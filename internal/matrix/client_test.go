package matrix

import (
	"context"
	"errors"
	"testing"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/cryptohelper"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

type fakeAPI struct {
	sentRoomID  id.RoomID
	sentType    event.Type
	sentContent any
	syncErr     error
	stopped     bool
}

func (f *fakeAPI) SendMessageEvent(
	_ context.Context,
	roomID id.RoomID,
	eventType event.Type,
	contentJSON any,
	_ ...mautrix.ReqSendEvent,
) (*mautrix.RespSendEvent, error) {
	f.sentRoomID = roomID
	f.sentType = eventType
	f.sentContent = contentJSON
	return &mautrix.RespSendEvent{EventID: "$reply"}, nil
}

func (f *fakeAPI) SyncWithContext(context.Context) error { return f.syncErr }
func (f *fakeAPI) StopSync()                             { f.stopped = true }

type fakeHandler struct {
	msgs []Message
	err  error
}

func (f *fakeHandler) HandleMatrixMessage(_ context.Context, msg Message) error {
	f.msgs = append(f.msgs, msg)
	return f.err
}

type fakeCrypto struct {
	decrypted *event.Event
	err       error
	calls     int
}

func (f *fakeCrypto) Decrypt(_ context.Context, _ *event.Event) (*event.Event, error) {
	f.calls++
	return f.decrypted, f.err
}

type fakeMautrixCrypto struct {
	fakeCrypto
}

func (f *fakeMautrixCrypto) Encrypt(context.Context, id.RoomID, event.Type, any) (*event.EncryptedEventContent, error) {
	return nil, nil
}

func (f *fakeMautrixCrypto) WaitForSession(context.Context, id.RoomID, id.SenderKey, id.SessionID, time.Duration) bool {
	return false
}

func (f *fakeMautrixCrypto) RequestSession(context.Context, id.RoomID, id.SenderKey, id.SessionID, id.UserID, id.DeviceID) {
}

func (f *fakeMautrixCrypto) Init(context.Context) error {
	return nil
}

func TestSendReply_Threaded(t *testing.T) {
	api := &fakeAPI{}
	handler := &fakeHandler{}
	c := &Client{api: api, handler: handler}

	err := c.SendReply(context.Background(), Reply{RoomID: "!room:test", InReplyToEventID: "$parent", Body: "hello", Thread: true})
	if err != nil {
		t.Fatalf("SendReply failed: %v", err)
	}

	if api.sentRoomID != "!room:test" || api.sentType != event.EventMessage {
		t.Fatalf("unexpected send envelope room=%s type=%s", api.sentRoomID, api.sentType)
	}

	content, ok := api.sentContent.(*event.MessageEventContent)
	if !ok {
		t.Fatalf("expected MessageEventContent, got %T", api.sentContent)
	}
	if content.MsgType != event.MsgNotice || content.Body != "hello" {
		t.Fatalf("unexpected content: %#v", content)
	}
	if content.RelatesTo == nil || content.RelatesTo.Type != event.RelThread || content.RelatesTo.EventID != "$parent" {
		t.Fatalf("expected thread relation to parent, got %#v", content.RelatesTo)
	}
}

func TestSendReply_EmptyBody(t *testing.T) {
	c := &Client{api: &fakeAPI{}, handler: &fakeHandler{}}
	if err := c.SendReply(context.Background(), Reply{RoomID: "!room:test", Body: "   "}); err == nil {
		t.Fatal("expected empty-body error")
	}
}

func TestForwardIfMessage_FiltersAndForwards(t *testing.T) {
	handler := &fakeHandler{}
	c := &Client{api: &fakeAPI{}, handler: handler, roomPolicy: AllowedRooms{"!allowed:test": {}}, botUserID: "@bot:test"}

	c.forwardIfMessage(context.Background(), &event.Event{Type: event.EventMessage, RoomID: "!blocked:test", ID: "$1", Sender: "@alice:test", Content: event.Content{Parsed: &event.MessageEventContent{MsgType: event.MsgText, Body: "hello"}}})
	c.forwardIfMessage(context.Background(), &event.Event{Type: event.EventMessage, RoomID: "!allowed:test", ID: "$2", Sender: "@bot:test", Content: event.Content{Parsed: &event.MessageEventContent{MsgType: event.MsgText, Body: "hello"}}})
	c.forwardIfMessage(context.Background(), &event.Event{Type: event.EventMessage, RoomID: "!allowed:test", ID: "$3", Sender: "@alice:test", Content: event.Content{Parsed: &event.MessageEventContent{MsgType: event.MsgText, Body: "  hello world  "}}})

	if len(handler.msgs) != 1 {
		t.Fatalf("expected one forwarded message, got %d", len(handler.msgs))
	}
	got := handler.msgs[0]
	if got.RoomID != "!allowed:test" || got.EventID != "$3" || got.Sender != "@alice:test" || got.Body != "hello world" {
		t.Fatalf("unexpected forwarded message: %#v", got)
	}
}

func TestOnEncryptedEvent_DecryptsAndForwards(t *testing.T) {
	handler := &fakeHandler{}
	dec := &event.Event{Type: event.EventMessage, RoomID: "!allowed:test", ID: "$d", Sender: "@alice:test", Content: event.Content{Parsed: &event.MessageEventContent{MsgType: event.MsgText, Body: "secret"}}}
	c := &Client{
		handler:    handler,
		roomPolicy: AllowedRooms{"!allowed:test": {}},
		botUserID:  "@bot:test",
		crypto:     &fakeCrypto{decrypted: dec},
	}

	c.onEncryptedEvent(context.Background(), &event.Event{Type: event.EventEncrypted, RoomID: "!allowed:test", ID: "$enc"})
	if len(handler.msgs) != 1 || handler.msgs[0].Body != "secret" {
		t.Fatalf("expected decrypted message to be forwarded, got %#v", handler.msgs)
	}
}

func TestNewClient_RegistersEncryptedFallbackWhenNotUsingCryptoHelper(t *testing.T) {
	mx, err := mautrix.NewClient("https://example.com", "@bot:test", "token")
	if err != nil {
		t.Fatalf("create mautrix client: %v", err)
	}

	handler := &fakeHandler{}
	dec := &event.Event{
		Type:    event.EventMessage,
		RoomID:  "!allowed:test",
		ID:      "$d",
		Sender:  "@alice:test",
		Content: event.Content{Parsed: &event.MessageEventContent{MsgType: event.MsgText, Body: "secret"}},
	}
	fake := &fakeMautrixCrypto{fakeCrypto: fakeCrypto{decrypted: dec}}
	mx.Crypto = fake

	_, err = NewClient(mx, AllowedRooms{"!allowed:test": {}}, handler, nil)
	if err != nil {
		t.Fatalf("new matrix client: %v", err)
	}

	syncer := mx.Syncer.(*mautrix.DefaultSyncer)
	syncer.Dispatch(context.Background(), &event.Event{
		Type:   event.EventEncrypted,
		RoomID: "!allowed:test",
		ID:     "$enc",
		Sender: "@alice:test",
	})

	if fake.calls != 1 {
		t.Fatalf("expected encrypted fallback decrypt call count 1, got %d", fake.calls)
	}
	if len(handler.msgs) != 1 || handler.msgs[0].Body != "secret" {
		t.Fatalf("expected decrypted message to be forwarded, got %#v", handler.msgs)
	}
}

func TestNewClient_DoesNotRegisterEncryptedFallbackWithCryptoHelper(t *testing.T) {
	mx, err := mautrix.NewClient("https://example.com", "@bot:test", "token")
	if err != nil {
		t.Fatalf("create mautrix client: %v", err)
	}
	mx.Crypto = &cryptohelper.CryptoHelper{}

	handler := &fakeHandler{}
	_, err = NewClient(mx, AllowedRooms{"!allowed:test": {}}, handler, nil)
	if err != nil {
		t.Fatalf("new matrix client: %v", err)
	}

	syncer := mx.Syncer.(*mautrix.DefaultSyncer)
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("dispatch panicked; encrypted fallback was unexpectedly invoked: %v", recovered)
		}
	}()

	syncer.Dispatch(context.Background(), &event.Event{
		Type:   event.EventEncrypted,
		RoomID: "!allowed:test",
		ID:     "$enc",
		Sender: "@alice:test",
	})

	if len(handler.msgs) != 0 {
		t.Fatalf("expected no forwarded messages, got %#v", handler.msgs)
	}
}

func TestStartStop(t *testing.T) {
	api := &fakeAPI{syncErr: errors.New("boom")}
	c := &Client{api: api, handler: &fakeHandler{}}
	if err := c.Start(context.Background()); err == nil {
		t.Fatal("expected sync error")
	}
	c.Stop()
	if !api.stopped {
		t.Fatal("expected StopSync to be called")
	}
}
