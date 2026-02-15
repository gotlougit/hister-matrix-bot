package matrix

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/crypto/cryptohelper"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

type fakeAPI struct {
	sentRoomID   id.RoomID
	sentType     event.Type
	sentContent  any
	stateRoomID  id.RoomID
	stateType    event.Type
	stateKey     string
	stateOut     any
	stateCalls   int
	stateErr     error
	joinedCalls  int
	joinedErr    error
	messagesResp *mautrix.RespMessages
	messagePages []*mautrix.RespMessages
	messagesErr  error
	messagesFrom []string
	messagesLim  []int
	syncErr      error
	stopped      bool
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
func (f *fakeAPI) StateEvent(_ context.Context, roomID id.RoomID, eventType event.Type, stateKey string, outContent interface{}) error {
	f.stateRoomID = roomID
	f.stateType = eventType
	f.stateKey = stateKey
	f.stateOut = outContent
	f.stateCalls++
	return f.stateErr
}
func (f *fakeAPI) JoinedMembers(_ context.Context, _ id.RoomID) (*mautrix.RespJoinedMembers, error) {
	f.joinedCalls++
	if f.joinedErr != nil {
		return nil, f.joinedErr
	}
	return &mautrix.RespJoinedMembers{}, nil
}
func (f *fakeAPI) Messages(_ context.Context, _ id.RoomID, from, _ string, _ mautrix.Direction, _ *mautrix.FilterPart, limit int) (*mautrix.RespMessages, error) {
	f.messagesFrom = append(f.messagesFrom, from)
	f.messagesLim = append(f.messagesLim, limit)
	if f.messagesErr != nil {
		return nil, f.messagesErr
	}
	if len(f.messagePages) > 0 {
		resp := f.messagePages[0]
		f.messagePages = f.messagePages[1:]
		return resp, nil
	}
	if f.messagesResp == nil {
		return &mautrix.RespMessages{}, nil
	}
	return f.messagesResp, nil
}

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

type fakeCryptoNeedsParsedEncrypted struct {
	decrypted *event.Event
	calls     int
}

func (f *fakeCryptoNeedsParsedEncrypted) Decrypt(_ context.Context, ev *event.Event) (*event.Event, error) {
	f.calls++
	if ev == nil {
		return nil, errors.New("nil event")
	}
	if _, ok := ev.Content.Parsed.(*event.EncryptedEventContent); !ok {
		return nil, errors.New("event content is not instance of *event.EncryptedEventContent")
	}
	return f.decrypted, nil
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

func TestSendReply_IgnoresMissingEncryptionState(t *testing.T) {
	api := &fakeAPI{stateErr: mautrix.MNotFound}
	c := &Client{api: api, handler: &fakeHandler{}}

	err := c.SendReply(context.Background(), Reply{RoomID: "!room:test", Body: "hello"})
	if err != nil {
		t.Fatalf("SendReply failed: %v", err)
	}
	if api.stateCalls != 1 || api.stateType != event.StateEncryption {
		t.Fatalf("expected one encryption state lookup, calls=%d type=%s", api.stateCalls, api.stateType)
	}
}

func TestSendReply_FailsWhenEncryptionStateLookupFails(t *testing.T) {
	api := &fakeAPI{stateErr: errors.New("boom")}
	c := &Client{api: api, handler: &fakeHandler{}}

	err := c.SendReply(context.Background(), Reply{RoomID: "!room:test", Body: "hello"})
	if err == nil {
		t.Fatal("expected SendReply to fail")
	}
	if api.sentRoomID != "" || api.sentContent != nil {
		t.Fatalf("expected no send call after state lookup failure, got room=%s content=%T", api.sentRoomID, api.sentContent)
	}
}

func TestSendReply_EncryptedRoomFetchesJoinedMembersWhenMissing(t *testing.T) {
	api := &fakeAPI{}
	stateStore := mautrix.NewMemoryStateStore()
	if err := stateStore.SetEncryptionEvent(context.Background(), "!room:test", &event.EncryptionEventContent{Algorithm: id.AlgorithmMegolmV1}); err != nil {
		t.Fatalf("SetEncryptionEvent failed: %v", err)
	}

	c := &Client{
		api:        api,
		handler:    &fakeHandler{},
		crypto:     &fakeCrypto{},
		stateStore: stateStore,
	}
	err := c.SendReply(context.Background(), Reply{RoomID: "!room:test", Body: "hello"})
	if err != nil {
		t.Fatalf("SendReply failed: %v", err)
	}
	if api.joinedCalls != 1 {
		t.Fatalf("expected one joined_members fetch, got %d", api.joinedCalls)
	}
}

func TestSendReply_EncryptedRoomSharesGroupSession(t *testing.T) {
	api := &fakeAPI{}
	stateStore := mautrix.NewMemoryStateStore()
	roomID := id.RoomID("!room:test")
	if err := stateStore.SetEncryptionEvent(context.Background(), roomID, &event.EncryptionEventContent{Algorithm: id.AlgorithmMegolmV1}); err != nil {
		t.Fatalf("SetEncryptionEvent failed: %v", err)
	}
	if err := stateStore.SetMember(context.Background(), roomID, "@alice:test", &event.MemberEventContent{Membership: event.MembershipJoin}); err != nil {
		t.Fatalf("SetMember failed: %v", err)
	}
	if err := stateStore.MarkMembersFetched(context.Background(), roomID); err != nil {
		t.Fatalf("MarkMembersFetched failed: %v", err)
	}

	calls := 0
	c := &Client{
		api:        api,
		handler:    &fakeHandler{},
		crypto:     &fakeCrypto{},
		stateStore: stateStore,
		shareGroup: func(_ context.Context, gotRoom id.RoomID, users []id.UserID) error {
			calls++
			if gotRoom != roomID {
				t.Fatalf("unexpected room id in shareGroup: %s", gotRoom)
			}
			if len(users) != 1 || users[0] != "@alice:test" {
				t.Fatalf("unexpected users in shareGroup: %#v", users)
			}
			return nil
		},
	}
	err := c.SendReply(context.Background(), Reply{RoomID: roomID, Body: "hello"})
	if err != nil {
		t.Fatalf("SendReply failed: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected one shareGroup call, got %d", calls)
	}
}

func TestSendReply_EncryptedRoomRotatesSessionBeforeShare(t *testing.T) {
	api := &fakeAPI{}
	stateStore := mautrix.NewMemoryStateStore()
	roomID := id.RoomID("!room:test")
	if err := stateStore.SetEncryptionEvent(context.Background(), roomID, &event.EncryptionEventContent{Algorithm: id.AlgorithmMegolmV1}); err != nil {
		t.Fatalf("SetEncryptionEvent failed: %v", err)
	}
	if err := stateStore.SetMember(context.Background(), roomID, "@alice:test", &event.MemberEventContent{Membership: event.MembershipJoin}); err != nil {
		t.Fatalf("SetMember failed: %v", err)
	}
	if err := stateStore.MarkMembersFetched(context.Background(), roomID); err != nil {
		t.Fatalf("MarkMembersFetched failed: %v", err)
	}

	rotated := false
	shared := false
	c := &Client{
		api:        api,
		handler:    &fakeHandler{},
		crypto:     &fakeCrypto{},
		stateStore: stateStore,
		resetGroup: func(_ context.Context, gotRoom id.RoomID) error {
			if gotRoom != roomID {
				t.Fatalf("unexpected room id in resetGroup: %s", gotRoom)
			}
			rotated = true
			return nil
		},
		shareGroup: func(_ context.Context, _ id.RoomID, _ []id.UserID) error {
			if !rotated {
				t.Fatal("shareGroup called before resetGroup")
			}
			shared = true
			return nil
		},
	}
	err := c.SendReply(context.Background(), Reply{RoomID: roomID, Body: "hello"})
	if err != nil {
		t.Fatalf("SendReply failed: %v", err)
	}
	if !rotated || !shared {
		t.Fatalf("expected rotated=%v and shared=%v to both be true", rotated, shared)
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

func TestNewClient_RegistersStateStoreSyncHandler(t *testing.T) {
	mx, err := mautrix.NewClient("https://example.com", "@bot:test", "token")
	if err != nil {
		t.Fatalf("create mautrix client: %v", err)
	}
	mx.StateStore = mautrix.NewMemoryStateStore()

	handler := &fakeHandler{}
	_, err = NewClient(mx, AllowedRooms{"!allowed:test": {}}, handler, nil)
	if err != nil {
		t.Fatalf("new matrix client: %v", err)
	}

	emptyStateKey := ""
	syncer := mx.Syncer.(*mautrix.DefaultSyncer)
	syncer.Dispatch(context.Background(), &event.Event{
		Type:     event.StateEncryption,
		RoomID:   "!allowed:test",
		StateKey: &emptyStateKey,
		Content: event.Content{
			Parsed: &event.EncryptionEventContent{
				Algorithm: id.AlgorithmMegolmV1,
			},
		},
	})

	encrypted, err := mx.StateStore.IsEncrypted(context.Background(), "!allowed:test")
	if err != nil {
		t.Fatalf("state store IsEncrypted failed: %v", err)
	}
	if !encrypted {
		t.Fatal("expected room to be marked encrypted in state store")
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

func TestGetRecentTextMessages_FiltersByTypeAndTime(t *testing.T) {
	now := time.Now().UTC()
	api := &fakeAPI{
		messagesResp: &mautrix.RespMessages{
			Chunk: []*event.Event{
				{Type: event.EventMessage, Sender: "@alice:test", Timestamp: now.Add(-10 * time.Minute).UnixMilli(), Content: event.Content{VeryRaw: json.RawMessage(`{"msgtype":"m.text","body":"hello"}`)}},
				{Type: event.EventMessage, Sender: "@bob:test", Timestamp: now.Add(-26 * time.Hour).UnixMilli(), Content: event.Content{VeryRaw: json.RawMessage(`{"msgtype":"m.text","body":"old"}`)}},
				{Type: event.EventMessage, Sender: "@carol:test", Timestamp: now.Add(-5 * time.Minute).UnixMilli(), Content: event.Content{VeryRaw: json.RawMessage(`{"msgtype":"m.image","body":"img"}`)}},
			},
		},
	}
	c := &Client{api: api, handler: &fakeHandler{}}

	msgs, err := c.GetRecentTextMessages(context.Background(), "!room:test", now.Add(-24*time.Hour), 40)
	if err != nil {
		t.Fatalf("GetRecentTextMessages failed: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 recent text message, got %d", len(msgs))
	}
	if msgs[0].Sender != "@alice:test" || msgs[0].Body != "hello" {
		t.Fatalf("unexpected message: %#v", msgs[0])
	}
}

func TestGetRecentTextMessages_DecryptsEncryptedEvents(t *testing.T) {
	now := time.Now().UTC()
	api := &fakeAPI{
		messagesResp: &mautrix.RespMessages{
			Chunk: []*event.Event{
				{
					Type:      event.EventEncrypted,
					RoomID:    "!room:test",
					ID:        "$enc",
					Sender:    "@alice:test",
					Timestamp: now.Add(-3 * time.Minute).UnixMilli(),
					Content: event.Content{VeryRaw: json.RawMessage(`{
						"algorithm":"m.megolm.v1.aes-sha2",
						"ciphertext":"abc",
						"device_id":"DEVICE",
						"sender_key":"key",
						"session_id":"sess"
					}`)},
				},
			},
		},
	}
	crypto := &fakeCryptoNeedsParsedEncrypted{
		decrypted: &event.Event{
			Type:      event.EventMessage,
			RoomID:    "!room:test",
			ID:        "$dec",
			Sender:    "@alice:test",
			Timestamp: now.Add(-3 * time.Minute).UnixMilli(),
			Content:   event.Content{VeryRaw: json.RawMessage(`{"msgtype":"m.text","body":"secret hello"}`)},
		},
	}
	c := &Client{api: api, handler: &fakeHandler{}, crypto: crypto}

	msgs, err := c.GetRecentTextMessages(context.Background(), "!room:test", now.Add(-24*time.Hour), 40)
	if err != nil {
		t.Fatalf("GetRecentTextMessages failed: %v", err)
	}
	if crypto.calls != 1 {
		t.Fatalf("expected one decrypt call, got %d", crypto.calls)
	}
	if len(msgs) != 1 || msgs[0].Body != "secret hello" {
		t.Fatalf("unexpected messages: %#v", msgs)
	}
}

func TestGetRecentTextMessages_PaginatesToFindMatchingMessages(t *testing.T) {
	now := time.Now().UTC()
	api := &fakeAPI{
		messagePages: []*mautrix.RespMessages{
			{
				End: "token-1",
				Chunk: []*event.Event{
					{Type: event.EventMessage, Sender: "@alice:test", Timestamp: now.Add(-2 * time.Minute).UnixMilli(), Content: event.Content{VeryRaw: json.RawMessage(`{"msgtype":"m.image","body":"img"}`)}},
				},
			},
			{
				Chunk: []*event.Event{
					{Type: event.EventMessage, Sender: "@bob:test", Timestamp: now.Add(-5 * time.Minute).UnixMilli(), Content: event.Content{VeryRaw: json.RawMessage(`{"msgtype":"m.text","body":"actual text"}`)}},
				},
			},
		},
	}
	c := &Client{api: api, handler: &fakeHandler{}}

	msgs, err := c.GetRecentTextMessages(context.Background(), "!room:test", now.Add(-24*time.Hour), 1)
	if err != nil {
		t.Fatalf("GetRecentTextMessages failed: %v", err)
	}
	if len(api.messagesFrom) < 2 || api.messagesFrom[0] != "END" || api.messagesFrom[1] != "token-1" {
		t.Fatalf("unexpected pagination tokens: %#v", api.messagesFrom)
	}
	if len(msgs) != 1 || msgs[0].Sender != "@bob:test" || msgs[0].Body != "actual text" {
		t.Fatalf("unexpected messages: %#v", msgs)
	}
}
