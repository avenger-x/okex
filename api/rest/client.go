package rest

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/amir-the-h/okex"
	requests "github.com/amir-the-h/okex/requests/rest/public"
	"github.com/amir-the-h/okex/responses"
	publicresponses "github.com/amir-the-h/okex/responses/public_data"
)

// ClientRest is the rest api client
type ClientRest struct {
	Account     *Account
	SubAccount  *SubAccount
	Trade       *Trade
	Funding     *Funding
	Market      *Market
	PublicData  *PublicData
	TradeData   *TradeData
	apiKey      string
	secretKey   []byte
	passphrase  string
	destination okex.Destination
	baseURL     okex.BaseURL
	client      *http.Client
}

// NewClient returns a fresh ClientRest. An optional HTTP client can provide a
// timeout, proxy, or transport. Without one, a new client uses the default transport.
// Configure an injected client before use; this constructor never modifies it.
func NewClient(apiKey, secretKey, passphrase string, baseURL okex.BaseURL, destination okex.Destination, httpClient ...*http.Client) *ClientRest {
	client := &http.Client{}
	if len(httpClient) > 0 && httpClient[0] != nil {
		client = httpClient[0]
	}
	c := &ClientRest{
		apiKey:      apiKey,
		secretKey:   []byte(secretKey),
		passphrase:  passphrase,
		baseURL:     baseURL,
		destination: destination,
		client:      client,
	}
	c.Account = NewAccount(c)
	c.SubAccount = NewSubAccount(c)
	c.Trade = NewTrade(c)
	c.Funding = NewFunding(c)
	c.Market = NewMarket(c)
	c.PublicData = NewPublicData(c)
	c.TradeData = NewTradeData(c)
	return c
}

// Do the http request to the server
func (c *ClientRest) Do(method, path string, private bool, params ...map[string]string) (*http.Response, error) {
	return c.DoWithContext(context.Background(), method, path, private, params...)
}

// DoWithContext signs and executes a request using only the supplied request's
// context. Successful response bodies belong to the caller; HTTP error bodies
// are closed here and included (up to 64 KiB) in HTTPError.
func (c *ClientRest) DoWithContext(ctx context.Context, method, path string, private bool, params ...map[string]string) (*http.Response, error) {
	var (
		body    io.Reader
		payload []byte
		err     error
	)
	if method != http.MethodGet && len(params) > 0 {
		payload, err = json.Marshal(params[0])
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(payload)
	}
	r, err := http.NewRequestWithContext(ctx, method, string(c.baseURL)+path, body)
	if err != nil {
		return nil, err
	}
	if method == http.MethodGet {
		if len(params) > 0 {
			q := r.URL.Query()
			for k, v := range params[0] {
				q.Add(k, strings.ReplaceAll(v, "\"", ""))
			}
			r.URL.RawQuery = q.Encode()
		}
	} else {
		r.Header.Add("Content-Type", "application/json")
	}
	if private {
		timestamp, sign := c.sign(r.Method, r.URL.RequestURI(), string(payload))
		r.Header.Add("OK-ACCESS-KEY", c.apiKey)
		r.Header.Add("OK-ACCESS-PASSPHRASE", c.passphrase)
		r.Header.Add("OK-ACCESS-SIGN", sign)
		r.Header.Add("OK-ACCESS-TIMESTAMP", timestamp)
	}
	if c.destination == okex.DemoServer {
		r.Header.Add("x-simulated-trading", "1")
	}
	res, err := c.client.Do(r)
	if err != nil {
		return nil, err
	}
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		defer res.Body.Close()
		body, readErr := io.ReadAll(io.LimitReader(res.Body, 64<<10))
		httpErr := &HTTPError{StatusCode: res.StatusCode, Status: res.Status, Body: string(body)}
		if readErr != nil {
			return nil, fmt.Errorf("%w (reading body: %w)", httpErr, readErr)
		}
		return nil, httpErr
	}
	return res, nil
}

// HTTPError reports a non-2xx response, including a bounded copy of its body.
type HTTPError struct {
	StatusCode int
	Status     string
	Body       string
}

func (e *HTTPError) Error() string {
	if e.Status != "" {
		return "unexpected HTTP status: " + e.Status
	}
	return fmt.Sprintf("unexpected HTTP status: %d", e.StatusCode)
}

// APIError reports a nonzero exchange response code. Code and Msg also remain
// available on the typed response returned by the query method.
type APIError struct {
	Code int
	Msg  string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("OKX error %d: %s", e.Code, e.Msg)
}

// decodeListResponse validates the response envelope as well as its typed data,
// so malformed responses cannot be mistaken for a successful empty result.
func decodeListResponse[T any](res *http.Response, status *responses.Basic, target *[]T) error {
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	var envelope struct {
		Code *int            `json:"code,string"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("decode response envelope: %w", err)
	}
	if envelope.Code == nil {
		return fmt.Errorf("decode response: missing code")
	}
	*status = responses.Basic{Code: *envelope.Code, Msg: envelope.Msg}
	if status.Code != 0 {
		return &APIError{Code: status.Code, Msg: status.Msg}
	}
	data := bytes.TrimSpace(envelope.Data)
	if len(data) == 0 || data[0] != '[' {
		return fmt.Errorf("decode response: data must be an array")
	}
	var items []T
	if err := json.Unmarshal(data, &items); err != nil {
		return fmt.Errorf("decode response data: %w", err)
	}
	*target = items
	return nil
}

// Status
// Get event status of system upgrade
//
// https://www.okex.com/docs-v5/en/#rest-api-status
func (c *ClientRest) Status(req requests.Status) (response publicresponses.Status, err error) {
	p := "/api/v5/system/status"
	m := okex.S2M(req)
	res, err := c.Do(http.MethodGet, p, false, m)
	if err != nil {
		return
	}
	defer res.Body.Close()
	d := json.NewDecoder(res.Body)
	err = d.Decode(&response)
	return
}

func (c *ClientRest) sign(method, path, body string) (string, string) {
	format := "2006-01-02T15:04:05.999Z07:00"
	t := time.Now().UTC().Format(format)
	ts := fmt.Sprint(t)
	s := ts + method + path + body
	p := []byte(s)
	h := hmac.New(sha256.New, c.secretKey)
	h.Write(p)
	return ts, base64.StdEncoding.EncodeToString(h.Sum(nil))
}
