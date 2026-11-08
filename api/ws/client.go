package ws

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/amir-the-h/okex"
	"github.com/amir-the-h/okex/events"
	"github.com/gorilla/websocket"
)

// ClientWs is the websocket API client. Configure channels before connecting or
// use the setters to change them while connected. The caller owns event channels.
type ClientWs struct {
	// Cancel cancels the client and waits for all connection workers and dispatches.
	Cancel              context.CancelFunc
	DoneChan            chan interface{}
	StructuredEventChan chan interface{}
	RawEventChan        chan *events.Basic
	ErrChan             chan *events.Error
	SubscribeChan       chan *events.Subscribe
	UnsubscribeCh       chan *events.Unsubscribe
	LoginChan           chan *events.Login
	SuccessChan         chan *events.Success
	// Use IsAuthorized for concurrent access to authorization state.
	AuthRequested *time.Time
	Authorized    bool
	Private       *Private
	Public        *Public
	Trade         *Trade
	ctx           context.Context
	done          chan struct{}
	wg            sync.WaitGroup
	mu            sync.Mutex
	channelMu     sync.RWMutex
	authMu        sync.RWMutex
	authError     error
	changed       chan struct{}
	running       map[bool]bool
	sessions      map[bool]*connection
	subscriptions map[bool]map[string]map[string]string
	url           map[bool]okex.BaseURL
	dialer        *websocket.Dialer
	apiKey        string
	secretKey     []byte
	passphrase    string
}

type connection struct {
	conn         *websocket.Conn
	ctx          context.Context
	cancel       context.CancelFunc
	send         chan outgoing
	lastActivity atomic.Int64
}

type outgoing struct {
	data   []byte
	op     okex.Operation
	args   []map[string]string
	result chan error
}

const (
	redialTick = 2 * time.Second
	writeWait  = 3 * time.Second
	pongWait   = 30 * time.Second
	PingPeriod = (pongWait * 8) / 10
)

func NewClient(ctx context.Context, apiKey, secretKey, passphrase string, urls map[bool]okex.BaseURL) *ClientWs {
	ctx, cancel := context.WithCancel(ctx)
	c := &ClientWs{
		apiKey: apiKey, secretKey: []byte(secretKey), passphrase: passphrase,
		ctx: ctx, done: make(chan struct{}), changed: make(chan struct{}),
		url:     map[bool]okex.BaseURL{false: urls[false], true: urls[true]},
		running: make(map[bool]bool), sessions: make(map[bool]*connection),
		subscriptions: map[bool]map[string]map[string]string{false: {}, true: {}},
		DoneChan:      make(chan interface{}, 1), dialer: websocket.DefaultDialer,
	}
	c.Private = NewPrivate(c)
	c.Public = NewPublic(c)
	c.Trade = NewTrade(c)
	c.Cancel = func() { cancel(); c.Wait() }
	go func() {
		<-ctx.Done()
		c.mu.Lock()
		for _, s := range c.sessions {
			s.cancel()
			_ = s.conn.Close()
		}
		c.mu.Unlock()
		c.wg.Wait()
		// Completion must never depend on a DoneChan consumer.
		select {
		case c.DoneChan <- "cancelled":
		default:
		}
		close(c.done)
	}()
	return c
}

// Close cancels the client, closes sockets, and waits for workers and dispatches.
// It does not close caller-owned event channels. It is safe to call repeatedly.
func (c *ClientWs) Close() error {
	c.Cancel()
	return nil
}

// Wait waits for shutdown, including when the parent context was cancelled.
func (c *ClientWs) Wait() { <-c.done }

// Connect establishes a connection, retrying until connected or cancelled.
// One supervisor per endpoint also reconnects and restores subscriptions.
func (c *ClientWs) Connect(p bool) error {
	c.mu.Lock()
	for {
		if err := c.ctx.Err(); err != nil {
			c.mu.Unlock()
			return err
		}
		if s := c.sessions[p]; s != nil && s.ctx.Err() == nil {
			c.mu.Unlock()
			return nil
		}
		if !c.running[p] {
			c.running[p] = true
			c.wg.Add(1)
			go c.run(p)
		}
		changed := c.changed
		c.mu.Unlock()
		select {
		case <-c.ctx.Done():
			return c.ctx.Err()
		case <-changed:
		}
		c.mu.Lock()
	}
}

// Login connects the private endpoint, which logs in on every connection.
func (c *ClientWs) Login() error { return c.Connect(true) }

func (c *ClientWs) IsAuthorized() bool {
	c.authMu.RLock()
	defer c.authMu.RUnlock()
	return c.Authorized
}

func (c *ClientWs) Subscribe(p bool, ch []okex.ChannelName, args ...map[string]string) error {
	chCount := max(len(ch), 1)
	tmpArgs := make([]map[string]string, 0, chCount*len(args))
	for i := 0; i < chCount; i++ {
		for _, arg := range args {
			item := make(map[string]string, len(arg)+1)
			for k, v := range arg {
				item[k] = v
			}
			if len(ch) > 0 {
				item["channel"] = string(ch[i])
			}
			tmpArgs = append(tmpArgs, item)
		}
	}
	return c.Send(p, okex.SubscribeOperation, tmpArgs)
}

func (c *ClientWs) Unsubscribe(p bool, ch []okex.ChannelName, args map[string]string) error {
	tmpArgs := make([]map[string]string, 0, max(len(ch), 1))
	for i := 0; i < max(len(ch), 1); i++ {
		item := make(map[string]string, len(args)+1)
		for k, v := range args {
			item[k] = v
		}
		if len(ch) > 0 {
			item["channel"] = string(ch[i])
		}
		tmpArgs = append(tmpArgs, item)
	}
	return c.Send(p, okex.UnsubscribeOperation, tmpArgs)
}

// Send waits until the message is written or the connection is cancelled.
// Trading requests are never replayed automatically after a disconnect.
func (c *ClientWs) Send(p bool, op okex.Operation, args []map[string]string, extras ...map[string]string) error {
	if op == okex.LoginOperation {
		return c.Login()
	}
	if err := c.Connect(p); err != nil {
		return err
	}
	if p {
		if err := c.WaitForAuthorization(); err != nil {
			return err
		}
	}
	copied := make([]map[string]string, len(args))
	for i, arg := range args {
		copied[i] = make(map[string]string, len(arg))
		for k, v := range arg {
			copied[i][k] = v
		}
	}
	data := map[string]interface{}{"op": op, "args": copied}
	for _, extra := range extras {
		for k, v := range extra {
			data[k] = v
		}
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	c.mu.Lock()
	s := c.sessions[p]
	c.mu.Unlock()
	if s == nil {
		return fmt.Errorf("websocket disconnected")
	}
	msg := outgoing{data: payload, op: op, args: copied, result: make(chan error, 1)}
	select {
	case <-s.ctx.Done():
		return s.ctx.Err()
	case s.send <- msg:
	}
	select {
	case err := <-msg.result:
		if ctxErr := s.ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return err
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

func (c *ClientWs) SetChannels(errCh chan *events.Error, subCh chan *events.Subscribe, unSub chan *events.Unsubscribe, lCh chan *events.Login, sCh chan *events.Success) {
	c.channelMu.Lock()
	defer c.channelMu.Unlock()
	c.ErrChan, c.SubscribeChan, c.UnsubscribeCh, c.LoginChan, c.SuccessChan = errCh, subCh, unSub, lCh, sCh
}

func (c *ClientWs) SetDialer(dialer *websocket.Dialer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dialer = dialer
}

func (c *ClientWs) SetEventChannels(structured chan interface{}, raw chan *events.Basic) {
	c.channelMu.Lock()
	defer c.channelMu.Unlock()
	c.StructuredEventChan, c.RawEventChan = structured, raw
}

func (c *ClientWs) WaitForAuthorization() error {
	if err := c.Connect(true); err != nil {
		return err
	}
	c.mu.Lock()
	s := c.sessions[true]
	c.mu.Unlock()
	if s == nil {
		return fmt.Errorf("websocket disconnected")
	}
	return c.waitForAuthorization(s.ctx)
}

func (c *ClientWs) waitForAuthorization(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, pongWait)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		c.authMu.RLock()
		authorized, authErr := c.Authorized, c.authError
		c.authMu.RUnlock()
		if authErr != nil {
			return authErr
		}
		if authorized {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// The pinned websocket version only applies DialContext cancellation to the TCP
// dial. Close the transport as well to interrupt proxy/TLS/HTTP handshakes.
func (c *ClientWs) dial(p bool) (*websocket.Conn, error) {
	c.mu.Lock()
	dialer := *c.dialer
	c.mu.Unlock()
	netDial := dialer.NetDialContext
	if netDial == nil {
		if dialer.NetDial != nil {
			netDial = func(_ context.Context, network, address string) (net.Conn, error) {
				return dialer.NetDial(network, address)
			}
		} else {
			netDial = (&net.Dialer{}).DialContext
		}
	}
	var stop func() bool
	var closed chan struct{}
	dialer.NetDialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := netDial(ctx, network, address)
		if err == nil {
			closed = make(chan struct{})
			stop = context.AfterFunc(c.ctx, func() {
				_ = conn.Close()
				close(closed)
			})
		}
		return conn, err
	}
	conn, res, err := dialer.DialContext(c.ctx, string(c.url[p]), nil)
	if stop != nil && !stop() {
		<-closed
	}
	if res != nil && res.Body != nil {
		_ = res.Body.Close()
	}
	return conn, err
}

func (c *ClientWs) run(p bool) {
	defer c.wg.Done()
	for c.ctx.Err() == nil {
		conn, err := c.dial(p)
		if err == nil {
			ctx, cancel := context.WithCancel(c.ctx)
			s := &connection{conn: conn, ctx: ctx, cancel: cancel, send: make(chan outgoing, 3)}
			s.lastActivity.Store(time.Now().UnixNano())
			c.mu.Lock()
			if c.ctx.Err() != nil {
				c.mu.Unlock()
				cancel()
				_ = conn.Close()
				return
			}
			c.sessions[p] = s
			close(c.changed)
			c.changed = make(chan struct{})
			c.mu.Unlock()
			results := make(chan error, 2)
			go func() { results <- c.sender(p, s) }()
			go func() { results <- c.receiver(s) }()
			<-results
			cancel()
			_ = conn.Close()
			<-results // No old receiver or delivery may overlap the next connection.
			c.mu.Lock()
			delete(c.sessions, p)
			if p {
				c.authMu.Lock()
				c.Authorized = false
				c.AuthRequested = nil
				c.authError = nil
				c.authMu.Unlock()
			}
			c.mu.Unlock()
		}
		timer := time.NewTimer(redialTick)
		select {
		case <-c.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (c *ClientWs) sender(p bool, s *connection) error {
	write := func(data []byte) error {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		if err := s.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
			return err
		}
		if err := s.conn.WriteMessage(websocket.TextMessage, data); err != nil {
			return err
		}
		s.lastActivity.Store(time.Now().UnixNano())
		return nil
	}
	if p {
		ts, sign := c.sign(http.MethodGet, "/users/self/verify")
		payload, err := json.Marshal(map[string]interface{}{"op": okex.LoginOperation, "args": []map[string]string{{"apiKey": c.apiKey, "passphrase": c.passphrase, "timestamp": ts, "sign": sign}}})
		if err != nil {
			return err
		}
		c.authMu.Lock()
		now := time.Now()
		c.AuthRequested = &now
		c.Authorized = false
		c.authError = nil
		c.authMu.Unlock()
		if err := write(payload); err != nil {
			return err
		}
		if err := c.waitForAuthorization(s.ctx); err != nil {
			return err
		}
	}
	c.mu.Lock()
	keys := make([]string, 0, len(c.subscriptions[p]))
	for key := range c.subscriptions[p] {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	args := make([]map[string]string, 0, len(keys))
	for _, key := range keys {
		args = append(args, c.subscriptions[p][key])
	}
	c.mu.Unlock()
	if len(args) > 0 {
		payload, err := json.Marshal(map[string]interface{}{"op": okex.SubscribeOperation, "args": args})
		if err != nil {
			return err
		}
		if err := write(payload); err != nil {
			return err
		}
	}
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return s.ctx.Err()
		case msg := <-s.send:
			err := write(msg.data)
			if err == nil && (msg.op == okex.SubscribeOperation || msg.op == okex.UnsubscribeOperation) {
				c.mu.Lock()
				for _, arg := range msg.args {
					key, _ := json.Marshal(arg)
					if msg.op == okex.SubscribeOperation {
						c.subscriptions[p][string(key)] = arg
					} else {
						delete(c.subscriptions[p], string(key))
					}
				}
				c.mu.Unlock()
			}
			msg.result <- err
			if err != nil {
				return err
			}
		case <-ticker.C:
			if time.Since(time.Unix(0, s.lastActivity.Load())) >= PingPeriod {
				if err := write([]byte("ping")); err != nil {
					return err
				}
			}
		}
	}
}

func (c *ClientWs) receiver(s *connection) error {
	for {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		if err := s.conn.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
			return err
		}
		mt, data, err := s.conn.ReadMessage()
		if err != nil {
			return err
		}
		s.lastActivity.Store(time.Now().UnixNano())
		if mt == websocket.TextMessage && string(data) != "pong" {
			e := &events.Basic{}
			if err := json.Unmarshal(data, e); err != nil {
				return err
			}
			c.process(s.ctx, data, e)
		}
	}
}

func (c *ClientWs) sign(method, path string) (string, string) {
	ts := fmt.Sprint(time.Now().UTC().Unix())
	h := hmac.New(sha256.New, c.secretKey)
	h.Write([]byte(ts + method + path))
	return ts, base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// deliver preserves backpressure and order while allowing shutdown to unblock
// a consumer that has stopped reading. Nil channels disable that event stream.
func deliver[T any](ctx context.Context, ch chan T, value T) bool {
	if ctx.Err() != nil {
		return false
	}
	if ch == nil {
		return true
	}
	select {
	case <-ctx.Done():
		return false
	case ch <- value:
		return true
	}
}

func (c *ClientWs) process(ctx context.Context, data []byte, e *events.Basic) bool {
	c.channelMu.RLock()
	errCh, subCh, unsubCh, loginCh, successCh := c.ErrChan, c.SubscribeChan, c.UnsubscribeCh, c.LoginChan, c.SuccessChan
	structured, raw := c.StructuredEventChan, c.RawEventChan
	c.channelMu.RUnlock()
	switch e.Event {
	case "error":
		event := events.Error{}
		if json.Unmarshal(data, &event) != nil {
			return false
		}
		deliver(ctx, errCh, &event)
		return true
	case "subscribe":
		event := events.Subscribe{}
		if json.Unmarshal(data, &event) != nil {
			return false
		}
		if deliver(ctx, subCh, &event) {
			deliver(ctx, structured, interface{}(event))
		}
		return true
	case "unsubscribe":
		event := events.Unsubscribe{}
		if json.Unmarshal(data, &event) != nil {
			return false
		}
		if deliver(ctx, unsubCh, &event) {
			deliver(ctx, structured, interface{}(event))
		}
		return true
	case "login":
		event := events.Login{}
		if json.Unmarshal(data, &event) != nil {
			return false
		}
		c.authMu.Lock()
		c.Authorized = event.Code == "0"
		c.AuthRequested = nil
		if event.Code != "0" {
			c.authError = fmt.Errorf("websocket login failed: %s: %s", event.Code, event.Msg)
		}
		c.authMu.Unlock()
		if deliver(ctx, loginCh, &event) {
			deliver(ctx, structured, interface{}(event))
		}
		return true
	}
	if c.Private.process(ctx, data, e) || c.Public.process(ctx, data, e) {
		return true
	}
	if e.ID != "" {
		if e.Code != 0 {
			ee := *e
			ee.Event = "error"
			return c.process(ctx, data, &ee)
		}
		event := events.Success{}
		if json.Unmarshal(data, &event) != nil {
			return false
		}
		if deliver(ctx, successCh, &event) {
			deliver(ctx, structured, interface{}(event))
		}
		return true
	}
	deliver(ctx, raw, e)
	return false
}
