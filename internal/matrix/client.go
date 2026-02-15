package matrix

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/cryptohelper"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

type Logger interface {
	Printf(format string, args ...any)
}

type RoomPolicy interface {
	Allowed(roomID id.RoomID) bool
}

type AllowedRooms map[id.RoomID]struct{}

func NewAllowedRooms(roomIDs []string) (AllowedRooms, error) {
	if len(roomIDs) == 0 {
		return nil, errors.New("at least one allowed room must be configured")
	}
	allowed := make(AllowedRooms, len(roomIDs))
	for i, room := range roomIDs {
		room = strings.TrimSpace(room)
		if room == "" {
			return nil, fmt.Errorf("allowed room at index %d is empty", i)
		}
		if !strings.HasPrefix(room, "!") {
			return nil, fmt.Errorf("allowed room %q must start with '!': invalid room id", room)
		}
		allowed[id.RoomID(room)] = struct{}{}
	}
	return allowed, nil
}

func (a AllowedRooms) Allowed(roomID id.RoomID) bool {
	if len(a) == 0 {
		return false
	}
	_, ok := a[roomID]
	return ok
}

type Message struct {
	RoomID  id.RoomID
	EventID id.EventID
	Sender  id.UserID
	Body    string
}

type MessageHandler interface {
	HandleMatrixMessage(ctx context.Context, msg Message) error
}

type MessageHandlerFunc func(ctx context.Context, msg Message) error

func (f MessageHandlerFunc) HandleMatrixMessage(ctx context.Context, msg Message) error {
	return f(ctx, msg)
}

type Reply struct {
	RoomID           id.RoomID
	InReplyToEventID id.EventID
	Body             string
	Thread           bool
}

type Config struct {
	HomeserverURL string
	UserID        id.UserID
	AccessToken   string
	DeviceID      id.DeviceID
	SyncTimeout   time.Duration
}

type Stores struct {
	SyncStore  mautrix.SyncStore
	StateStore mautrix.StateStore
	Crypto     mautrix.CryptoHelper
}

type EventDecrypter interface {
	Decrypt(ctx context.Context, evt *event.Event) (*event.Event, error)
}

type matrixAPI interface {
	SendMessageEvent(
		ctx context.Context,
		roomID id.RoomID,
		eventType event.Type,
		contentJSON any,
		extra ...mautrix.ReqSendEvent,
	) (*mautrix.RespSendEvent, error)
	Messages(ctx context.Context, roomID id.RoomID, from, to string, dir mautrix.Direction, filter *mautrix.FilterPart, limit int) (*mautrix.RespMessages, error)
	SyncWithContext(ctx context.Context) error
	StopSync()
}

type Client struct {
	api        matrixAPI
	crypto     EventDecrypter
	roomPolicy RoomPolicy
	handler    MessageHandler
	logger     Logger
	botUserID  id.UserID
}

func BuildMautrixClient(cfg Config, stores Stores) (*mautrix.Client, error) {
	if strings.TrimSpace(cfg.HomeserverURL) == "" {
		return nil, errors.New("homeserver URL is required")
	}
	if cfg.UserID == "" {
		return nil, errors.New("user ID is required")
	}
	if strings.TrimSpace(cfg.AccessToken) == "" {
		return nil, errors.New("access token is required")
	}

	mx, err := mautrix.NewClient(cfg.HomeserverURL, cfg.UserID, cfg.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("create mautrix client: %w", err)
	}

	if cfg.DeviceID != "" {
		mx.DeviceID = cfg.DeviceID
	}
	if stores.SyncStore != nil {
		mx.Store = stores.SyncStore
	}
	if stores.StateStore != nil {
		mx.StateStore = stores.StateStore
	}
	if stores.Crypto != nil {
		mx.Crypto = stores.Crypto
	}

	ensureDefaultSyncer(mx)
	return mx, nil
}

func NewClient(
	mx *mautrix.Client,
	roomPolicy RoomPolicy,
	handler MessageHandler,
	logger Logger,
) (*Client, error) {
	if mx == nil {
		return nil, errors.New("mautrix client is required")
	}
	if handler == nil {
		return nil, errors.New("message handler is required")
	}

	c := &Client{
		api:        mx,
		crypto:     mx.Crypto,
		roomPolicy: roomPolicy,
		handler:    handler,
		logger:     logger,
		botUserID:  mx.UserID,
	}

	syncer := ensureDefaultSyncer(mx)
	syncer.OnEventType(event.EventMessage, c.onMessageEvent)
	if !usesCryptoHelperAutoDecrypt(mx.Crypto) {
		syncer.OnEventType(event.EventEncrypted, c.onEncryptedEvent)
	}

	return c, nil
}

func (c *Client) Start(ctx context.Context) error {
	if err := c.api.SyncWithContext(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("matrix sync failed: %w", err)
	}
	return nil
}

func (c *Client) Stop() {
	c.api.StopSync()
}

func (c *Client) SendReply(ctx context.Context, reply Reply) error {
	body := strings.TrimSpace(reply.Body)
	if body == "" {
		return errors.New("reply body must not be empty")
	}

	content := &event.MessageEventContent{
		MsgType: event.MsgNotice,
		Body:    body,
	}

	if reply.InReplyToEventID != "" {
		parent := &event.Event{ID: reply.InReplyToEventID, RoomID: reply.RoomID}
		if reply.Thread {
			content.SetThread(parent)
		} else {
			content.SetReply(parent)
		}
	}

	_, err := c.api.SendMessageEvent(ctx, reply.RoomID, event.EventMessage, content)
	if err != nil {
		return fmt.Errorf("send matrix reply: %w", err)
	}
	return nil
}

func (c *Client) onMessageEvent(ctx context.Context, ev *event.Event) {
	c.forwardIfMessage(ctx, ev)
}

func (c *Client) onEncryptedEvent(ctx context.Context, ev *event.Event) {
	if ev == nil {
		return
	}
	if c.crypto == nil {
		c.logf("received encrypted event without crypto helper room=%s event=%s", ev.RoomID, ev.ID)
		return
	}

	decrypted, err := c.crypto.Decrypt(ctx, ev)
	if err != nil {
		c.logf("decrypt failed room=%s event=%s err=%v", ev.RoomID, ev.ID, err)
		return
	}
	c.forwardIfMessage(ctx, decrypted)
}

func (c *Client) forwardIfMessage(ctx context.Context, ev *event.Event) {
	if ev == nil || c.handler == nil {
		return
	}
	if c.botUserID != "" && ev.Sender == c.botUserID {
		return
	}
	if c.roomPolicy != nil && !c.roomPolicy.Allowed(ev.RoomID) {
		return
	}
	if ev.Type != event.EventMessage {
		return
	}

	content := ev.Content.AsMessage()
	if content == nil || !content.MsgType.IsText() {
		return
	}

	body := strings.TrimSpace(content.Body)
	if body == "" {
		return
	}

	err := c.handler.HandleMatrixMessage(ctx, Message{RoomID: ev.RoomID, EventID: ev.ID, Sender: ev.Sender, Body: body})
	if err != nil {
		c.logf("message handler failed room=%s event=%s err=%v", ev.RoomID, ev.ID, err)
	}
}

func ensureDefaultSyncer(mx *mautrix.Client) *mautrix.DefaultSyncer {
	if syncer, ok := mx.Syncer.(*mautrix.DefaultSyncer); ok && syncer != nil {
		syncer.ParseEventContent = true
		return syncer
	}

	syncer := mautrix.NewDefaultSyncer()
	syncer.ParseEventContent = true
	mx.Syncer = syncer
	return syncer
}

func usesCryptoHelperAutoDecrypt(decrypter EventDecrypter) bool {
	if decrypter == nil {
		return false
	}
	_, ok := decrypter.(*cryptohelper.CryptoHelper)
	return ok
}

func (c *Client) logf(format string, args ...any) {
	if c.logger != nil {
		c.logger.Printf(format, args...)
	}
}
