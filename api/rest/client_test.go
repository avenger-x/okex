package rest_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/amir-the-h/okex"
	"github.com/amir-the-h/okex/api"
	"github.com/amir-the-h/okex/api/rest"
	account "github.com/amir-the-h/okex/requests/rest/account"
	market "github.com/amir-the-h/okex/requests/rest/market"
	public "github.com/amir-the-h/okex/requests/rest/public"
	"github.com/amir-the-h/okex/responses"
	marketresponses "github.com/amir-the-h/okex/responses/market"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestExactRESTDecimalsAndSigning(t *testing.T) {
	const precise = "12345678901234567890.12345678901234567890"
	const tiny = "0.000000000000000000000000123456789"
	const negative = "-0.000000000123456789"
	const zero = "0.00000000"
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var uri, payload string
		switch r.URL.Path {
		case "/api/v5/account/balance":
			uri = "/api/v5/account/balance?ccy=BTC%2CUSDT"
			payload = `{"code":"0","data":[{"totalEq":"` + precise + `","adjEq":"","isoEq":"` + zero + `","details":[{"ccy":"BTC","eq":"` + precise + `","cashBal":"` + tiny + `","availBal":"","frozenBal":"` + zero + `","liab":"` + negative + `","upl":"` + negative + `"}]}]}`
		case "/api/v5/account/positions":
			uri = "/api/v5/account/positions?instId=BTC-USDT-SWAP%2CETH-USDT-SWAP&instType=SWAP&posId=123%2C456"
			payload = `{"code":"0","data":[{"instId":"BTC-USDT-SWAP","pos":"` + negative + `","availPos":"` + zero + `","avgPx":"` + precise + `","markPx":"` + tiny + `","upl":"` + negative + `","lever":"10.0000","liqPx":"","margin":"` + zero + `","liab":"` + negative + `"}]}`
		case "/api/v5/public/instruments":
			uri = "/api/v5/public/instruments?instId=BTC-USDT-SWAP&instType=SWAP"
			payload = `{"code":"0","data":[{"instId":"BTC-USDT-SWAP","ctVal":"` + tiny + `","ctMult":"1.23000000","ctValCcy":"BTC","ctType":"linear","settleCcy":"USDT"},{"ctVal":"","ctMult":"0"}]}`
		default:
			t.Fatalf("unexpected request: %s", r.URL)
		}
		if r.Method != http.MethodGet || r.URL.RequestURI() != uri || r.Body != nil {
			t.Fatalf("request = %s %s, body %v; want GET %s without a body", r.Method, r.URL.RequestURI(), r.Body, uri)
		}
		if r.URL.Path == "/api/v5/public/instruments" {
			for _, header := range []string{"OK-ACCESS-KEY", "OK-ACCESS-PASSPHRASE", "OK-ACCESS-SIGN", "OK-ACCESS-TIMESTAMP"} {
				if r.Header.Get(header) != "" {
					t.Errorf("public request has private header %s", header)
				}
			}
		} else {
			assertSignature(t, r, "GET"+uri)
		}
		if got := r.Header.Get("x-simulated-trading"); got != "1" {
			t.Errorf("demo header = %q, want 1", got)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(payload))}, nil
	})
	client := rest.NewClient("key", "secret", "pass", "https://example.test", okex.DemoServer, &http.Client{Transport: transport})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	balance, err := client.Account.GetBalanceWithContext(ctx, account.GetBalance{Ccy: []string{"BTC", "USDT"}})
	if err != nil || len(balance.Balances) != 1 || len(balance.Balances[0].Details) != 1 {
		t.Fatalf("balance = %+v, err = %v", balance, err)
	}
	b := balance.Balances[0]
	d := b.Details[0]
	if got, want := [...]string{b.TotalEq, b.AdjEq, b.IsoEq, d.Ccy, d.Eq, d.CashBal, d.AvailBal, d.FrozenBal, d.Liab, d.Upl}, [...]string{precise, "", zero, "BTC", precise, tiny, "", zero, negative, negative}; got != want {
		t.Errorf("balance values = %q, want %q", got, want)
	}

	positions, err := client.Account.GetPositionsWithContext(ctx, account.GetPositions{
		InstType: okex.SwapInstrument, InstID: []string{"BTC-USDT-SWAP", "ETH-USDT-SWAP"}, PosID: []string{"123", "456"},
	})
	if err != nil || len(positions.Positions) != 1 {
		t.Fatalf("positions = %+v, err = %v", positions, err)
	}
	p := positions.Positions[0]
	if got, want := [...]string{p.Pos, p.AvailPos, p.AvgPx, p.MarkPx, p.Upl, p.Lever, p.LiqPx, p.Margin, p.Liab}, [...]string{negative, zero, precise, tiny, negative, "10.0000", "", zero, negative}; got != want {
		t.Errorf("position values = %q, want %q", got, want)
	}

	instruments, err := client.PublicData.GetInstrumentsWithContext(ctx, public.GetInstruments{InstID: "BTC-USDT-SWAP", InstType: okex.SwapInstrument})
	if err != nil || len(instruments.Instruments) != 2 {
		t.Fatalf("instruments = %+v, err = %v", instruments, err)
	}
	i := instruments.Instruments[0]
	if i.CtVal != tiny || i.CtMult != "1.23000000" || i.CtValCcy != "BTC" || i.CtType != okex.ContractLinearType || i.SettleCcy != "USDT" {
		t.Errorf("contract spec = %+v", i)
	}
	if i := instruments.Instruments[1]; i.CtVal != "" || i.CtMult != "0" {
		t.Errorf("contract spec lost the distinction between empty and zero: %+v", i)
	}
}

func assertSignature(t *testing.T, r *http.Request, signedPayload string) {
	t.Helper()
	timestamp := r.Header.Get("OK-ACCESS-TIMESTAMP")
	if _, err := time.Parse(time.RFC3339Nano, timestamp); err != nil {
		t.Errorf("invalid timestamp %q: %v", timestamp, err)
	}
	h := hmac.New(sha256.New, []byte("secret"))
	_, _ = io.WriteString(h, timestamp+signedPayload)
	if got, want := r.Header.Get("OK-ACCESS-SIGN"), base64.StdEncoding.EncodeToString(h.Sum(nil)); got != want {
		t.Errorf("signature = %q, want %q", got, want)
	}
	if r.Header.Get("OK-ACCESS-KEY") != "key" || r.Header.Get("OK-ACCESS-PASSPHRASE") != "pass" {
		t.Error("missing authentication headers")
	}
}

func TestDoWithContextSignsBodyAsSent(t *testing.T) {
	for _, params := range []map[string]string{{"amt": "1.2300"}, {}} {
		client := rest.NewClient("key", "secret", "pass", "https://example.test", okex.NormalServer, &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			defer r.Body.Close()
			body, err := io.ReadAll(r.Body)
			if err != nil {
				return nil, err
			}
			want, _ := json.Marshal(params)
			if string(body) != string(want) || r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("request body = %q, want JSON %q", body, want)
			}
			assertSignature(t, r, "POST/example?mode=test"+string(want))
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
		})})
		res, err := client.DoWithContext(context.Background(), http.MethodPost, "/example?mode=test", true, params)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
	}
}

func TestRequestCancellationIsolatedFromOtherCalls(t *testing.T) {
	started := make(chan struct{})
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/v5/account/balance" {
			close(started)
			<-r.Context().Done()
			return nil, r.Context().Err()
		}
		if err := r.Context().Err(); err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"code":"0","data":[]}`))}, nil
	})}
	client, err := api.NewClient(context.Background(), "key", "secret", "pass", okex.NormalServer, httpClient)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Ws.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := client.Rest.Account.GetBalanceWithContext(ctx, account.GetBalance{})
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("request never reached injected transport")
	}
	if _, err := client.Rest.PublicData.GetInstruments(public.GetInstruments{InstType: okex.SwapInstrument}); err != nil {
		t.Fatalf("query while the other request is in flight: %v", err)
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled request = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("request did not stop on cancellation")
	}
	positions, err := client.Rest.Account.GetPositions(account.GetPositions{})
	if err != nil || positions.Positions == nil || len(positions.Positions) != 0 {
		t.Fatalf("legacy request after cancellation = %+v, %v; want a successful empty list", positions, err)
	}
}

type contextBody struct{ ctx context.Context }

func (b contextBody) Read([]byte) (int, error) {
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func (b contextBody) Close() error { return nil }

func TestQueryTimeoutWhileReadingBody(t *testing.T) {
	for _, clientTimeout := range []bool{false, true} {
		name := "request context"
		if clientTimeout {
			name = "injected HTTP client"
		}
		t.Run(name, func(t *testing.T) {
			httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: contextBody{r.Context()}}, nil
			})}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if clientTimeout {
				httpClient.Timeout = 30 * time.Millisecond
			} else {
				requestCtx, requestCancel := context.WithTimeout(ctx, 30*time.Millisecond)
				defer requestCancel()
				ctx = requestCtx
			}
			client := rest.NewClient("", "", "", "https://example.test", okex.NormalServer, httpClient)
			start := time.Now()
			_, err := client.PublicData.GetInstrumentsWithContext(ctx, public.GetInstruments{InstType: okex.SwapInstrument})
			if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) >= time.Second {
				t.Fatalf("body read error = %v after %s, want deadline exceeded promptly", err, time.Since(start))
			}
		})
	}
}

type trackedBody struct {
	io.Reader
	closed bool
}

func (b *trackedBody) Close() error { b.closed = true; return nil }

func TestListResponseErrors(t *testing.T) {
	for _, test := range []struct {
		name, payload string
		status, code  int
		want          string
	}{
		{"HTTP error with valid empty data", `{"code":"0","data":[]}`, 429, 0, "HTTP"},
		{"HTTP error with non-JSON body", "bad gateway", 502, 0, "HTTP"},
		{"exchange error", `{"code":"50113","msg":"Invalid signature","data":[]}`, 200, 50113, "OKX"},
		{"exchange error with atypical data", `{"code":"50113","msg":"Invalid signature","data":{}}`, 200, 50113, "OKX"},
		{"broken JSON", `{"code":"0","data":[`, 200, 0, "envelope"},
		{"trailing JSON", `{"code":"0","data":[]} {}`, 200, 0, "envelope"},
		{"missing code", `{"data":[]}`, 200, 0, "missing code"},
		{"missing data", `{"code":"0"}`, 200, 0, "data must be an array"},
		{"null data", `{"code":"0","data":null}`, 200, 0, "data must be an array"},
		{"incorrect data type", `{"code":"0","data":{}}`, 200, 0, "data must be an array"},
		{"incorrect decimal type", `{"code":"0","data":[{"pos":"0"},{"pos":1.2345}]}`, 200, 0, "response data"},
		{"legitimate empty data", `{"code":"0","data":[]}`, 200, 0, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := &trackedBody{Reader: strings.NewReader(test.payload)}
			client := rest.NewClient("", "", "", "https://example.test", okex.NormalServer, &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: test.status, Body: body}, nil
			})})
			response, err := client.Account.GetPositions(account.GetPositions{})
			if !body.closed {
				t.Error("response body was not closed")
			}
			if test.want == "" {
				if err != nil || response.Positions == nil || len(response.Positions) != 0 {
					t.Fatalf("empty list = %+v, %v", response, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) || response.Positions != nil {
				t.Fatalf("response = %+v, error = %v; want error containing %q and no results", response, err, test.want)
			}
			if test.status != http.StatusOK {
				var httpErr *rest.HTTPError
				if !errors.As(err, &httpErr) || httpErr.StatusCode != test.status || httpErr.Body != test.payload {
					t.Errorf("unexpected HTTP error: %+v", httpErr)
				}
			}
			if test.code != 0 {
				var apiErr *rest.APIError
				if !errors.As(err, &apiErr) || apiErr.Code != test.code || apiErr.Msg != "Invalid signature" || response.Code != test.code || response.Msg != apiErr.Msg {
					t.Errorf("business error metadata lost: response = %+v, error = %v", response, err)
				}
			}
		})
	}
}

func TestMarketQueriesPreservePrecisionAndLegacyCalls(t *testing.T) {
	const (
		price    = "0.000000000123456789"
		size     = "123456789.123456789"
		trailing = "1.23000000"
	)
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy=%t", legacy), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			var bodies []*trackedBody
			client := rest.NewClient("key", "secret", "pass", "https://example.test", okex.NormalServer,
				&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					if legacy {
						if r.Context() != context.Background() {
							t.Error("legacy method did not use Background context")
						}
					} else if r.Context() != ctx {
						t.Error("request context was not propagated")
					}
					if r.Method != http.MethodGet || r.Body != nil || r.Header.Get("OK-ACCESS-KEY") != "" {
						t.Errorf("unexpected public GET request: %+v", r)
					}
					var payload, uri string
					switch r.URL.Path {
					case "/api/v5/market/tickers":
						uri = "/api/v5/market/tickers?instType=SWAP&uly=BTC-USDT"
						payload = `{"code":"0","msg":"ok","data":[{"instId":"BTC-USDT-SWAP","bidPx":"` + price + `","bidSz":"` + size + `","askPx":"` + trailing + `","askSz":"9007199254740993.123456789"}]}`
					case "/api/v5/market/books":
						uri = "/api/v5/market/books?instId=BTC-USDT-SWAP&sz=5"
						payload = `{"code":"0","msg":"ok","data":[{"asks":[["` + price + `","` + size + `","0","17"]],"bids":[["` + trailing + `","0.00000000","2","3"]]}]}`
					default:
						t.Fatalf("unexpected endpoint: %s", r.URL.Path)
					}
					if r.URL.RequestURI() != uri {
						t.Errorf("URI = %s, want %s", r.URL.RequestURI(), uri)
					}
					body := &trackedBody{Reader: strings.NewReader(payload)}
					bodies = append(bodies, body)
					return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
				})})
			tickerReq := market.GetTickers{InstType: okex.SwapInstrument, Uly: "BTC-USDT"}
			var tickers marketresponses.Ticker
			var err error
			if legacy {
				tickers, err = client.Market.GetTickers(tickerReq)
			} else {
				tickers, err = client.Market.GetTickersWithContext(ctx, tickerReq)
			}
			if err != nil || len(tickers.Tickers) != 1 {
				t.Fatalf("tickers = %+v, err = %v", tickers, err)
			}
			ticker := tickers.Tickers[0]
			if ticker.BidPx != price || ticker.BidSz != size || ticker.AskPx != trailing || ticker.AskSz != "9007199254740993.123456789" {
				t.Errorf("ticker decimals changed: %+v", ticker)
			}
			bookReq := market.GetOrderBook{InstID: "BTC-USDT-SWAP", Sz: 5}
			var books marketresponses.OrderBook
			if legacy {
				books, err = client.Market.GetOrderBook(bookReq)
			} else {
				books, err = client.Market.GetOrderBookWithContext(ctx, bookReq)
			}
			if err != nil || len(books.OrderBooks) != 1 {
				t.Fatalf("books = %+v, err = %v", books, err)
			}
			book := books.OrderBooks[0]
			if len(book.Asks) != 1 || len(book.Bids) != 1 {
				t.Fatalf("unexpected levels: %+v", book)
			}
			ask, bid := book.Asks[0], book.Bids[0]
			if ask.DepthPrice != price || ask.Size != size || ask.OrderNumbers != 17 || bid.DepthPrice != trailing || bid.Size != "0.00000000" || bid.LiquidatedOrder != 2 || bid.OrderNumbers != 3 {
				t.Errorf("order book values changed: ask=%+v bid=%+v", ask, bid)
			}
			if tickers.Code != 0 || tickers.Msg != "ok" || books.Code != 0 || books.Msg != "ok" {
				t.Error("response code/msg lost")
			}
			if len(bodies) != 2 {
				t.Errorf("request count = %d, want 2", len(bodies))
			}
			for _, body := range bodies {
				if !body.closed {
					t.Error("response body was not closed")
				}
			}
		})
	}
}

// Both endpoint adapters return their envelope even when parsing fails, allowing
// the same error and cancellation cases to exercise each public method.
var marketQueries = []struct {
	name string
	call func(*rest.ClientRest, context.Context, bool) (responses.Basic, int, error)
}{
	{"tickers", func(c *rest.ClientRest, ctx context.Context, legacy bool) (responses.Basic, int, error) {
		req := market.GetTickers{InstType: okex.SwapInstrument}
		if legacy {
			r, err := c.Market.GetTickers(req)
			return r.Basic, len(r.Tickers), err
		}
		r, err := c.Market.GetTickersWithContext(ctx, req)
		return r.Basic, len(r.Tickers), err
	}},
	{"order book", func(c *rest.ClientRest, ctx context.Context, legacy bool) (responses.Basic, int, error) {
		req := market.GetOrderBook{InstID: "BTC-USDT-SWAP"}
		if legacy {
			r, err := c.Market.GetOrderBook(req)
			return r.Basic, len(r.OrderBooks), err
		}
		r, err := c.Market.GetOrderBookWithContext(ctx, req)
		return r.Basic, len(r.OrderBooks), err
	}},
}

func TestMarketQueryErrorsAndBodyClosure(t *testing.T) {
	readErr := errors.New("response interrupted")
	for _, query := range marketQueries {
		for _, legacy := range []bool{false, true} {
			for _, tt := range []struct {
				name, payload string
				status        int
			}{
				{"business error", `{"code":"51000","msg":"Invalid request","data":[]}`, 200},
				{"business error without data", `{"code":"51000","msg":"Invalid request"}`, 200},
				{"HTTP error", `{"code":"0","data":[]}`, 503},
				{"malformed JSON", `{"code":"0","data":[`, 200},
				{"trailing JSON", `{"code":"0","data":[]} {}`, 200},
				{"missing code", `{"data":[]}`, 200},
				{"null code", `{"code":null,"data":[]}`, 200},
				{"invalid code", `{"code":"wrong","data":[]}`, 200},
				{"missing data", `{"code":"0"}`, 200},
				{"null data", `{"code":"0","data":null}`, 200},
				{"object data", `{"code":"0","data":{}}`, 200},
				{"numeric decimal", `{"code":"0","data":[{"bidPx":1.23,"asks":[[1.23,"1","0","1"]]}]}`, 200},
				{"read failure", "", 200},
				{"empty body", "", 200},
				{"empty list", `{"code":"0","data":[]}`, 200},
			} {
				t.Run(fmt.Sprintf("%s/legacy=%t/%s", query.name, legacy, tt.name), func(t *testing.T) {
					var reader io.Reader = strings.NewReader(tt.payload)
					if tt.name == "read failure" {
						reader = failingReader{readErr}
					}
					body := &trackedBody{Reader: reader}
					client := rest.NewClient("", "", "", "https://example.test", okex.NormalServer,
						&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
							return &http.Response{StatusCode: tt.status, Body: body}, nil
						})})
					basic, count, err := query.call(client, context.Background(), legacy)
					if !body.closed {
						t.Error("response body was not closed")
					}
					if tt.name == "empty list" {
						if err != nil || count != 0 {
							t.Fatalf("empty list: count=%d error=%v", count, err)
						}
						return
					}
					if err == nil || count != 0 {
						t.Fatalf("count=%d error=%v; want error without data", count, err)
					}
					if strings.HasPrefix(tt.name, "business error") {
						var apiErr *rest.APIError
						if !errors.As(err, &apiErr) || apiErr.Code != 51000 || apiErr.Msg != "Invalid request" || basic.Code != apiErr.Code || basic.Msg != apiErr.Msg {
							t.Errorf("business code/msg lost: response=%+v error=%v", basic, err)
						}
					}
					if tt.name == "HTTP error" {
						var httpErr *rest.HTTPError
						if !errors.As(err, &httpErr) || httpErr.StatusCode != tt.status {
							t.Errorf("HTTP status lost: %v", err)
						}
					}
					if tt.name == "read failure" && !errors.Is(err, readErr) {
						t.Errorf("read error lost: %v", err)
					}
				})
			}
		}
	}
}

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

func TestMarketQueryCancellationAndTimeout(t *testing.T) {
	for _, query := range marketQueries {
		for _, bodyRead := range []bool{false, true} {
			for _, timeout := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/body=%t/timeout=%t", query.name, bodyRead, timeout), func(t *testing.T) {
					ctx, cancel := context.WithCancel(context.Background())
					want := context.Canceled
					if timeout {
						cancel()
						ctx, cancel = context.WithTimeout(context.Background(), 30*time.Millisecond)
						want = context.DeadlineExceeded
					}
					defer cancel()
					var body *trackedBody
					client := rest.NewClient("", "", "", "https://example.test", okex.NormalServer,
						&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
							if r.Context().Done() == nil { // Subsequent legacy call must be independent.
								return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"code":"0","data":[]}`))}, nil
							}
							if !timeout {
								cancel()
							}
							if bodyRead {
								body = &trackedBody{Reader: contextBody{r.Context()}}
								return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
							}
							<-r.Context().Done()
							return nil, r.Context().Err()
						})})
					result := make(chan error, 1)
					go func() { _, _, err := query.call(client, ctx, false); result <- err }()
					select {
					case err := <-result:
						if !errors.Is(err, want) {
							t.Fatalf("error=%v, want %v", err, want)
						}
					case <-time.After(2 * time.Second):
						t.Fatal("request did not stop")
					}
					if bodyRead && (body == nil || !body.closed) {
						t.Error("cancelled response body was not closed")
					}
					if _, _, err := query.call(client, context.Background(), true); err != nil {
						t.Fatalf("legacy call after cancellation: %v", err)
					}
				})
			}
		}
	}
}
