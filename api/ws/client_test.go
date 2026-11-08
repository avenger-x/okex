package ws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/amir-the-h/okex"
	"github.com/amir-the-h/okex/events"
	privateevents "github.com/amir-the-h/okex/events/private"
	publicevents "github.com/amir-the-h/okex/events/public"
	requests "github.com/amir-the-h/okex/requests/ws/public"
	"github.com/gorilla/websocket"
)

const testTimeout = 8 * time.Second

func testClient(t *testing.T, handler func(*websocket.Conn)) *ClientWs {
	t.Helper()
	var mu sync.Mutex
	var connections []*websocket.Conn
	var workers sync.WaitGroup
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		workers.Add(1)
		defer workers.Done()
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		mu.Lock()
		connections = append(connections, conn)
		mu.Unlock()
		defer conn.Close()
		handler(conn)
	}))
	t.Cleanup(func() {
		server.Close()
		mu.Lock()
		for _, conn := range connections {
			_ = conn.Close()
		}
		mu.Unlock()
		workers.Wait()
	})
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	c := NewClient(ctx, "key", "secret", "pass", map[bool]okex.BaseURL{false: okex.BaseURL("ws" + strings.TrimPrefix(server.URL, "http")), true: okex.BaseURL("ws" + strings.TrimPrefix(server.URL, "http"))})
	t.Cleanup(func() { cancel(); c.Wait() })
	return c
}

func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(testTimeout):
		t.Fatal("timed out waiting for result")
		var zero T
		return zero
	}
}

func bookMessage(action string, seq, prev int64) []byte {
	return []byte(fmt.Sprintf(`{"arg":{"channel":"books","instId":"BTC-USDT"},"action":%q,"data":[{"asks":[["0.000000000123456789","123456789.123456789","0","1"]],"bids":[["1.23000000","1.23000000","0","2"]],"seqId":%d,"prevSeqId":%d,"ts":"1630048897897"}]}`, action, seq, prev))
}

func TestOrderBookReceiveOrder(t *testing.T) {
	const count = 256
	const firstSeq = int64(9007199254740993)
	c := testClient(t, func(conn *websocket.Conn) {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		for i := 0; i < count; i++ {
			action, prev := "update", firstSeq+int64(i)-1
			if i == 0 {
				action, prev = "snapshot", -1
			}
			if err := conn.WriteMessage(websocket.TextMessage, bookMessage(action, firstSeq+int64(i), prev)); err != nil {
				return
			}
		}
		_, _, _ = conn.ReadMessage()
	})
	books := make(chan *publicevents.OrderBook)
	structured := make(chan interface{})
	c.SetEventChannels(structured, nil)
	if err := c.Public.OrderBook([]requests.OrderBook{{InstID: "BTC-USDT", Channel: "books"}}, books); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < count; i++ {
		book := receive(t, books)
		mirror, ok := receive(t, structured).(publicevents.OrderBook)
		if !ok || len(book.Books) != 1 || len(mirror.Books) != 1 {
			t.Fatal("missing order book")
		}
		wantPrev := firstSeq + int64(i) - 1
		wantAction := "update"
		if i == 0 {
			wantPrev = -1
			wantAction = "snapshot"
		}
		for _, event := range []*publicevents.OrderBook{book, &mirror} {
			level := event.Books[0]
			if event.Action != wantAction || level.SeqID != firstSeq+int64(i) || level.PrevSeqID != wantPrev {
				t.Fatalf("message %d out of order: action=%s seq=%d prev=%d", i, event.Action, level.SeqID, level.PrevSeqID)
			}
			if level.Asks[0].DepthPrice != "0.000000000123456789" || level.Asks[0].Size != "123456789.123456789" || level.Bids[0].DepthPrice != "1.23000000" {
				t.Fatal("precision lost")
			}
		}
	}
	c.Cancel()
	select {
	case <-books:
		t.Fatal("delivery after Cancel returned")
	default:
	}
}

func TestReconnectRestoresSubscriptions(t *testing.T) {
	for _, private := range []bool{false, true} {
		private := private
		t.Run(fmt.Sprintf("private=%t", private), func(t *testing.T) {
			t.Parallel()
			var attempts atomic.Int32
			restored := make(chan []map[string]string, 1)
			c := testClient(t, func(conn *websocket.Conn) {
				generation := attempts.Add(1)
				var msg struct {
					Op   string              `json:"op"`
					Args []map[string]string `json:"args"`
				}
				if private {
					if err := conn.ReadJSON(&msg); err != nil {
						return
					}
					if msg.Op != "login" {
						t.Errorf("first operation = %q, want login", msg.Op)
						return
					}
					if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"event":"login","code":"0","msg":""}`)); err != nil {
						return
					}
					msg.Args = nil
				}
				if err := conn.ReadJSON(&msg); err != nil {
					return
				}
				if msg.Op != "subscribe" {
					t.Errorf("operation = %q, want subscribe", msg.Op)
					return
				}
				if generation == 1 {
					if len(msg.Args) != 2 {
						t.Errorf("initial subscriptions = %v", msg.Args)
						return
					}
					msg.Args = nil
					if err := conn.ReadJSON(&msg); err != nil {
						return
					}
					if msg.Op != "unsubscribe" {
						t.Errorf("operation = %q, want unsubscribe", msg.Op)
						return
					}
					_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"msg":"first connection"}`))
					return // Simulate transport loss; the new connection must reauthenticate.
				}
				restored <- msg.Args
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"msg":"restored connection"}`))
				_, _, _ = conn.ReadMessage()
			})
			raw := make(chan *events.Basic, 2)
			c.SetEventChannels(nil, raw)
			channel := "books"
			if private {
				channel = "orders"
			}
			if err := c.Subscribe(private, []okex.ChannelName{okex.ChannelName(channel)}, map[string]string{"instId": "BTC-USDT"}, map[string]string{"instId": "ETH-USDT"}); err != nil {
				t.Fatal(err)
			}
			if err := c.Unsubscribe(private, []okex.ChannelName{okex.ChannelName(channel)}, map[string]string{"instId": "ETH-USDT"}); err != nil {
				t.Fatal(err)
			}
			if msg := receive(t, raw); msg.Msg != "first connection" {
				t.Fatalf("unexpected first event: %+v", msg)
			}
			got := receive(t, restored)
			want := []map[string]string{{"channel": channel, "instId": "BTC-USDT"}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("restored subscriptions = %v, want %v", got, want)
			}
			if msg := receive(t, raw); msg.Msg != "restored connection" {
				t.Fatalf("unexpected recovery event: %+v", msg)
			}
			if attempts.Load() != 2 {
				t.Fatalf("connections = %d, want 2", attempts.Load())
			}
			if private && !c.IsAuthorized() {
				t.Fatal("private connection was not reauthorized")
			}
			c.Cancel()
		})
	}
}

func TestReconnectOrderBookStartsWithSnapshot(t *testing.T) {
	var attempts atomic.Int32
	c := testClient(t, func(conn *websocket.Conn) {
		generation := attempts.Add(1)
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		first := int64(generation) * 100
		if err := conn.WriteMessage(websocket.TextMessage, bookMessage("snapshot", first, -1)); err != nil {
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, bookMessage("update", first+1, first)); err != nil {
			return
		}
		if generation == 1 {
			return
		}
		_, _, _ = conn.ReadMessage()
	})
	books := make(chan *publicevents.OrderBook, 4)
	if err := c.Public.OrderBook([]requests.OrderBook{{InstID: "BTC-USDT", Channel: "books"}}, books); err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct {
		action    string
		seq, prev int64
	}{{"snapshot", 100, -1}, {"update", 101, 100}, {"snapshot", 200, -1}, {"update", 201, 200}} {
		event := receive(t, books)
		book := event.Books[0]
		if event.Action != want.action || book.SeqID != want.seq || book.PrevSeqID != want.prev {
			t.Fatalf("recovery order: action=%s seq=%d prev=%d, want %+v", event.Action, book.SeqID, book.PrevSeqID, want)
		}
	}
}

func TestCancelClosesBothConnectionsAndWaits(t *testing.T) {
	var attempts atomic.Int32
	closed := make(chan struct{}, 2)
	c := testClient(t, func(conn *websocket.Conn) {
		attempts.Add(1)
		defer func() {
			select {
			case closed <- struct{}{}:
			default:
			}
		}()
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if strings.Contains(string(data), `"login"`) {
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"event":"login","code":"0"}`))
			}
		}
	})
	if err := c.Connect(false); err != nil {
		t.Fatal(err)
	}
	if err := c.WaitForAuthorization(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); _ = c.Close() }()
		}
		wg.Wait()
		close(done)
	}()
	receive(t, done)
	receive(t, closed)
	receive(t, closed)
	c.mu.Lock()
	remaining := len(c.sessions)
	c.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("%d connection workers remain", remaining)
	}
	if c.IsAuthorized() {
		t.Fatal("authorization not cleared")
	}
	if err := c.Connect(false); !errors.Is(err, context.Canceled) {
		t.Fatalf("Connect after close = %v", err)
	}
	// Wait through a reconnect interval: no supervisor or heartbeat may restart.
	time.Sleep(redialTick + 100*time.Millisecond)
	if attempts.Load() != 2 {
		t.Fatalf("connections after cancellation = %d, want 2", attempts.Load())
	}
}

func TestParentCancellationUnblocksRead(t *testing.T) {
	closed := make(chan struct{})
	serverClient := testClient(t, func(conn *websocket.Conn) { _, _, _ = conn.ReadMessage(); close(closed) })
	ctx, cancel := context.WithCancel(context.Background())
	c := NewClient(ctx, "", "", "", serverClient.url)
	t.Cleanup(func() { cancel(); c.Wait() })
	if err := c.Connect(false); err != nil {
		t.Fatal(err)
	}
	cancel()
	done := make(chan struct{})
	go func() { c.Wait(); close(done) }()
	receive(t, done)
	receive(t, closed)
}

func TestCancelDuringAuthentication(t *testing.T) {
	loginReceived := make(chan struct{})
	c := testClient(t, func(conn *websocket.Conn) {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		close(loginReceived) // Deliberately never send the authentication response.
		_, _, _ = conn.ReadMessage()
	})
	result := make(chan error, 1)
	go func() {
		result <- c.Subscribe(true, []okex.ChannelName{"orders"}, map[string]string{"instType": "ANY"})
	}()
	receive(t, loginReceived)
	c.Cancel()
	if err := receive(t, result); !errors.Is(err, context.Canceled) {
		t.Fatalf("authorization cancellation = %v", err)
	}
}

func TestCancelDuringDialAndReconnect(t *testing.T) {
	for _, failFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("retry=%t", failFirst), func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			c := NewClient(ctx, "", "", "", map[bool]okex.BaseURL{false: "ws://example.invalid"})
			t.Cleanup(func() { cancel(); c.Wait() })
			started := make(chan struct{}, 1)
			var calls atomic.Int32
			c.SetDialer(&websocket.Dialer{NetDialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				call := calls.Add(1)
				started <- struct{}{}
				if failFirst && call == 1 {
					return nil, errors.New("dial failed")
				}
				<-ctx.Done()
				return nil, ctx.Err()
			}})
			result := make(chan error, 1)
			go func() { result <- c.Connect(false) }()
			receive(t, started)
			if failFirst {
				receive(t, started)
			}
			cancel()
			c.Wait()
			if err := receive(t, result); !errors.Is(err, context.Canceled) {
				t.Fatalf("Connect cancellation = %v", err)
			}
			want := int32(1)
			if failFirst {
				want = 2
			}
			if calls.Load() != want {
				t.Fatalf("dial calls = %d, want %d", calls.Load(), want)
			}
		})
	}
}

func TestFailedHandshakeRecovers(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			http.Error(w, "temporary failure", http.StatusServiceUnavailable)
			return
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	c := NewClient(ctx, "", "", "", map[bool]okex.BaseURL{false: okex.BaseURL("ws" + strings.TrimPrefix(server.URL, "http"))})
	defer c.Cancel()
	if err := c.Connect(false); err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("handshake attempts = %d, want 2", attempts.Load())
	}
}

func TestAllEventDeliveriesCancel(t *testing.T) {
	tests := []struct {
		name, payload string
		setup         func(*ClientWs)
	}{
		{"error", `{"event":"error","code":"1"}`, func(c *ClientWs) { c.ErrChan = make(chan *events.Error) }},
		{"subscribe", `{"event":"subscribe","arg":{"channel":"books"}}`, func(c *ClientWs) { c.SubscribeChan = make(chan *events.Subscribe) }},
		{"unsubscribe", `{"event":"unsubscribe","arg":{"channel":"books"}}`, func(c *ClientWs) { c.UnsubscribeCh = make(chan *events.Unsubscribe) }},
		{"login", `{"event":"login","code":"0"}`, func(c *ClientWs) { c.LoginChan = make(chan *events.Login) }},
		{"success", `{"id":"1","code":"0","data":[]}`, func(c *ClientWs) { c.SuccessChan = make(chan *events.Success) }},
		{"raw", `{"msg":"unknown"}`, func(c *ClientWs) { c.RawEventChan = make(chan *events.Basic) }},
		{"structured", `{"event":"subscribe"}`, func(c *ClientWs) { c.StructuredEventChan = make(chan interface{}) }},
		{"public", string(bookMessage("snapshot", 1, -1)), func(c *ClientWs) { c.Public.obCh = make(chan *publicevents.OrderBook) }},
		{"private account", `{"arg":{"channel":"account"},"data":[{}]}`, func(c *ClientWs) { c.Private.aCh = make(chan *privateevents.Account) }},
		{"private position", `{"arg":{"channel":"positions"},"data":[{}]}`, func(c *ClientWs) { c.Private.pCh = make(chan *privateevents.Position) }},
		{"private balance", `{"arg":{"channel":"balance_and_position"},"data":[{}]}`, func(c *ClientWs) { c.Private.bnpCh = make(chan *privateevents.BalanceAndPosition) }},
		{"private order", `{"arg":{"channel":"orders"},"data":[{}]}`, func(c *ClientWs) { c.Private.oCh = make(chan *privateevents.Order) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sent := make(chan struct{})
			c := testClient(t, func(conn *websocket.Conn) {
				if err := conn.WriteMessage(websocket.TextMessage, []byte(tt.payload)); err != nil {
					return
				}
				close(sent)
				_, _, _ = conn.ReadMessage()
			})
			tt.setup(c)
			if err := c.Connect(false); err != nil {
				t.Fatal(err)
			}
			receive(t, sent)
			// Allow dispatch to reach the deliberately unread, unbuffered channel.
			select {
			case <-c.done:
				t.Fatal("client unexpectedly stopped")
			case <-time.After(10 * time.Millisecond):
			}
			done := make(chan struct{})
			go func() { c.Cancel(); close(done) }()
			receive(t, done)
			// Callers can safely release their channels once shutdown completes.
			if c.Public.obCh != nil {
				close(c.Public.obCh)
			}
		})
	}
}

func TestChannelSettersWhileReceiving(t *testing.T) {
	c := testClient(t, func(conn *websocket.Conn) {
		for i := 0; i < 300; i++ {
			if err := conn.WriteMessage(websocket.TextMessage, bookMessage("update", int64(i+1), int64(i))); err != nil {
				return
			}
		}
		_, _, _ = conn.ReadMessage()
	})
	if err := c.Connect(false); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		c.SetChannels(nil, nil, nil, nil, nil)
		c.SetEventChannels(make(chan interface{}, 300), make(chan *events.Basic, 300))
		c.SetDialer(websocket.DefaultDialer)
		c.channelMu.Lock()
		c.Public.obCh = make(chan *publicevents.OrderBook, 300)
		c.channelMu.Unlock()
	}
	c.Cancel()
}

func TestOrderBookSequenceJSON(t *testing.T) {
	var event publicevents.OrderBook
	if err := json.Unmarshal(bookMessage("snapshot", 9007199254740993, -1), &event); err != nil {
		t.Fatal(err)
	}
	if event.Books[0].SeqID != 9007199254740993 || event.Books[0].PrevSeqID != -1 {
		t.Fatalf("sequence precision lost: %+v", event.Books[0])
	}
}

func TestCancelDuringHandshake(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release // Accept TCP but never finish the WebSocket handshake.
	}))
	defer server.Close()
	defer close(release)
	c := NewClient(context.Background(), "", "", "", map[bool]okex.BaseURL{false: okex.BaseURL("ws" + strings.TrimPrefix(server.URL, "http"))})
	defer c.Cancel()
	result := make(chan error, 1)
	go func() { result <- c.Connect(false) }()
	receive(t, started)
	done := make(chan struct{})
	go func() { c.Cancel(); close(done) }()
	receive(t, done)
	if err := receive(t, result); !errors.Is(err, context.Canceled) {
		t.Fatalf("handshake cancellation = %v", err)
	}
}

// blockedWriterConn lets the handshake finish, then simulates a network write
// that cannot complete until the underlying connection is closed.
type blockedWriterConn struct {
	net.Conn
	hold   atomic.Bool
	closed chan struct{}
	once   sync.Once
}

func (c *blockedWriterConn) Write(data []byte) (int, error) {
	if c.hold.Load() {
		<-c.closed
		return 0, net.ErrClosed
	}
	return c.Conn.Write(data)
}

func (c *blockedWriterConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

func TestCancelUnblocksPendingSends(t *testing.T) {
	c := testClient(t, func(conn *websocket.Conn) { _, _, _ = conn.ReadMessage() })
	transport := make(chan *blockedWriterConn, 1)
	c.SetDialer(&websocket.Dialer{NetDialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		wrapped := &blockedWriterConn{Conn: conn, closed: make(chan struct{})}
		transport <- wrapped
		return wrapped, nil
	}})
	if err := c.Connect(false); err != nil {
		t.Fatal(err)
	}
	receive(t, transport).hold.Store(true)
	const writers = 8
	result := make(chan error, writers)
	for i := 0; i < writers; i++ {
		go func() { result <- c.Send(false, okex.OrderOperation, []map[string]string{{"instId": "BTC-USDT"}}) }()
	}
	deadline := time.Now().Add(testTimeout)
	for {
		c.mu.Lock()
		s := c.sessions[false]
		pending := s != nil && len(s.send) == cap(s.send)
		c.mu.Unlock()
		if pending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("writer queue never filled")
		}
		time.Sleep(time.Millisecond)
	}
	c.Cancel()
	for i := 0; i < writers; i++ {
		if err := receive(t, result); !errors.Is(err, context.Canceled) {
			t.Fatalf("pending send cancellation = %v", err)
		}
	}
}

func TestHeartbeatStopsOnClose(t *testing.T) {
	ping := make(chan string, 1)
	peerClosed := make(chan struct{})
	c := testClient(t, func(conn *websocket.Conn) {
		defer close(peerClosed)
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		ping <- string(data)
		_, _, _ = conn.ReadMessage()
	})
	if err := c.Connect(false); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	c.sessions[false].lastActivity.Store(time.Now().Add(-PingPeriod).UnixNano())
	c.mu.Unlock()
	if got := receive(t, ping); got != "ping" {
		t.Fatalf("heartbeat = %q", got)
	}
	c.Cancel()
	receive(t, peerClosed)
}

func TestPrivateEventsReceiveOrder(t *testing.T) {
	const count = 128
	c := testClient(t, func(conn *websocket.Conn) {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"event":"login","code":"0"}`)); err != nil {
			return
		}
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		for i := 0; i < count; i++ {
			data := []byte(fmt.Sprintf(`{"arg":{"channel":"orders"},"data":[{"ordId":"%d"}]}`, i))
			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				return
			}
		}
		_, _, _ = conn.ReadMessage()
	})
	orders := make(chan *privateevents.Order)
	c.Private.oCh = orders
	if err := c.Subscribe(true, []okex.ChannelName{"orders"}, map[string]string{"instType": "ANY"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < count; i++ {
		event := receive(t, orders)
		if len(event.Orders) != 1 || event.Orders[0].OrdID != fmt.Sprint(i) {
			t.Fatalf("private event %d out of order: %+v", i, event)
		}
	}
}

func TestRejectedLoginDoesNotAuthorize(t *testing.T) {
	c := testClient(t, func(conn *websocket.Conn) {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"event":"login","code":"60009","msg":"Login failed"}`))
		_, _, _ = conn.ReadMessage()
	})
	if err := c.WaitForAuthorization(); err == nil {
		t.Fatal("rejected login unexpectedly succeeded")
	}
	if c.IsAuthorized() {
		t.Fatal("rejected login marked client authorized")
	}
}
