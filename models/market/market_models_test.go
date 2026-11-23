package market_test

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/amir-the-h/okex/events/public"
	"github.com/amir-the-h/okex/models/market"
	marketresponses "github.com/amir-the-h/okex/responses/market"
)

func TestOrderBookEntityUnmarshalJSONPrecision(t *testing.T) {
	values := []string{
		"0.000000000123456789",
		"123456789.123456789",
		"1.23000000",
		"9007199254740993.123456789",
		"0.00000000",
	}
	for _, price := range values {
		for _, size := range values {
			t.Run(price+"/"+size, func(t *testing.T) {
				payload := fmt.Sprintf(`[%q,%q,"2","17"]`, price, size)
				var entity market.OrderBookEntity
				if err := json.Unmarshal([]byte(payload), &entity); err != nil {
					t.Fatalf("Unmarshal() error = %v", err)
				}
				want := market.OrderBookEntity{
					DepthPrice: price, Size: size, LiquidatedOrder: 2, OrderNumbers: 17,
				}
				if entity != want {
					t.Errorf("Unmarshal() = %+v, want %+v", entity, want)
				}
			})
		}
	}
}

func TestTickerResponsesPreserveQuoteStrings(t *testing.T) {
	values := []string{"0.000000000123456789", "123456789.123456789", "1.23000000", "9007199254740993.123456789", "", "0", "0.00000000"}
	for i := range values {
		want := [4]string{values[i], values[(i+1)%len(values)], values[(i+2)%len(values)], values[(i+3)%len(values)]}
		data := fmt.Sprintf(`[{"instId":"BTC-USDT","bidPx":%q,"bidSz":%q,"askPx":%q,"askSz":%q}]`, want[0], want[1], want[2], want[3])
		t.Run(fmt.Sprintf("REST/%d", i), func(t *testing.T) {
			var response marketresponses.Ticker
			if err := json.Unmarshal([]byte(`{"code":"0","data":`+data+`}`), &response); err != nil {
				t.Fatal(err)
			}
			if len(response.Tickers) != 1 || response.Tickers[0] == nil {
				t.Fatalf("missing ticker: %+v", response)
			}
			ticker := response.Tickers[0]
			if got := [4]string{ticker.BidPx, ticker.BidSz, ticker.AskPx, ticker.AskSz}; got != want {
				t.Errorf("quotes = %q, want %q", got, want)
			}
		})
		t.Run(fmt.Sprintf("WebSocket/%d", i), func(t *testing.T) {
			var event public.Tickers
			if err := json.Unmarshal([]byte(`{"arg":{"channel":"tickers","instId":"BTC-USDT"},"data":`+data+`}`), &event); err != nil {
				t.Fatal(err)
			}
			if len(event.Tickers) != 1 || event.Tickers[0] == nil {
				t.Fatalf("missing ticker: %+v", event)
			}
			ticker := event.Tickers[0]
			if got := [4]string{ticker.BidPx, ticker.BidSz, ticker.AskPx, ticker.AskSz}; got != want {
				t.Errorf("quotes = %q, want %q", got, want)
			}
		})
	}
}

func TestOrderBookEntityUnmarshalJSONInvalid(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{"malformed JSON", `["1","2","0","3"`},
		{"missing field", `["1","2","0"]`},
		{"extra field", `["1","2","0","3","4"]`},
		{"numeric price", `[1,"2","0","3"]`},
		{"numeric size", `["1",2,"0","3"]`},
		{"invalid liquidated order", `["1","2","invalid","3"]`},
		{"fractional liquidated order", `["1","2","1.5","3"]`},
		{"invalid order numbers", `["1","2","0","invalid"]`},
		{"fractional order numbers", `["1","2","0","3.5"]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var entity market.OrderBookEntity
			if err := json.Unmarshal([]byte(tt.payload), &entity); err == nil {
				t.Fatal("Unmarshal() succeeded, want error")
			}
		})
	}
}

func TestOrderBookResponsesPreservePrecision(t *testing.T) {
	const book = `{
		"asks":[
			["0.000000000123456789","123456789.123456789","0","17"],
			["1.23000000","1.23000000","2","3"]
		],
		"bids":[
			["1.23000000","0.000000000123456789","0","4"],
			["0.000000000123456789","9007199254740993.123456789","1","5"]
		],
		"ts":"1630048897897",
		"checksum":1234
	}`
	wantAsks := []market.OrderBookEntity{
		{DepthPrice: "0.000000000123456789", Size: "123456789.123456789", LiquidatedOrder: 0, OrderNumbers: 17},
		{DepthPrice: "1.23000000", Size: "1.23000000", LiquidatedOrder: 2, OrderNumbers: 3},
	}
	wantBids := []market.OrderBookEntity{
		{DepthPrice: "1.23000000", Size: "0.000000000123456789", LiquidatedOrder: 0, OrderNumbers: 4},
		{DepthPrice: "0.000000000123456789", Size: "9007199254740993.123456789", LiquidatedOrder: 1, OrderNumbers: 5},
	}
	checkLevels := func(t *testing.T, side string, got []*market.OrderBookEntity, want []market.OrderBookEntity) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s: got %d levels, want %d", side, len(got), len(want))
		}
		for i, level := range got {
			if level == nil || *level != want[i] {
				t.Errorf("%s[%d] = %+v, want %+v", side, i, level, want[i])
			}
		}
	}

	t.Run("REST", func(t *testing.T) {
		payload := `{"code":"0","msg":"","data":[` + book + `]}`
		var response marketresponses.OrderBook
		if err := json.Unmarshal([]byte(payload), &response); err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}
		if len(response.OrderBooks) != 1 || response.OrderBooks[0] == nil {
			t.Fatalf("unexpected order books: %+v", response.OrderBooks)
		}
		checkLevels(t, "asks", response.OrderBooks[0].Asks, wantAsks)
		checkLevels(t, "bids", response.OrderBooks[0].Bids, wantBids)
	})

	for _, action := range []string{"snapshot", "update"} {
		t.Run("WebSocket/"+action, func(t *testing.T) {
			payload := `{"arg":{"channel":"books","instId":"BTC-USDT"},"action":` +
				fmt.Sprintf("%q", action) + `,"data":[` + book + `]}`
			var event public.OrderBook
			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}
			if len(event.Books) != 1 || event.Books[0] == nil {
				t.Fatalf("unexpected order books: %+v", event.Books)
			}
			checkLevels(t, "asks", event.Books[0].Asks, wantAsks)
			checkLevels(t, "bids", event.Books[0].Bids, wantBids)
			if event.Action != action || event.Books[0].Checksum != 1234 {
				t.Errorf("unexpected event metadata: action=%q, checksum=%d", event.Action, event.Books[0].Checksum)
			}
		})
	}
}

func TestCandlePreservesNineColumns(t *testing.T) {
	const (
		o        = "0.000000000123456789"
		h        = "123456789.123456789"
		l        = "0.000000000000000000001"
		closePx  = "1.23000000"
		vol      = "9007199254740993.123456789"
		volCcy   = "0.00000000"
		volQuote = "12345678901234567890.1234567890000"
	)
	for _, timestamp := range []int64{1630048897897, 9007199254740993} {
		for _, confirm := range []string{"0", "1"} {
			t.Run(fmt.Sprintf("ts=%d/confirm=%s", timestamp, confirm), func(t *testing.T) {
				row := fmt.Sprintf(`[%q,%q,%q,%q,%q,%q,%q,%q,%q]`, fmt.Sprint(timestamp), o, h, l, closePx, vol, volCcy, volQuote, confirm)
				var candle market.Candle
				if err := json.Unmarshal([]byte(row), &candle); err != nil {
					t.Fatal(err)
				}
				want := market.Candle{O: o, H: h, L: l, C: closePx, Vol: vol, VolCcy: volCcy, VolCcyQuote: volQuote, Confirm: confirm}
				want.TS = candle.TS
				if candle != want {
					t.Errorf("candle = %+v, want %+v", candle, want)
				}
				if got := time.Time(candle.TS).UnixMilli(); got != timestamp {
					t.Errorf("timestamp = %d, want %d", got, timestamp)
				}
			})
		}
	}
}

func TestCandleRejectsInvalidRows(t *testing.T) {
	valid := []interface{}{"1630048897897", "1.23000000", "2", "1", "2", "10", "20", "30", "1"}
	type rowCase struct {
		name string
		data []byte
	}
	tests := []rowCase{
		{"malformed JSON", []byte(`["1630048897897"`)},
		{"object", []byte(`{}`)},
		{"null row", []byte(`null`)},
		{"empty array", []byte(`[]`)},
	}
	for _, count := range []int{5, 7, 8, 10} {
		fields := append(append([]interface{}{}, valid...), "extra")[:count]
		row, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		tests = append(tests, rowCase{fmt.Sprintf("%d columns", count), row})
	}
	for i := range valid {
		for _, value := range []interface{}{nil, 123, true} {
			fields := append([]interface{}{}, valid...)
			fields[i] = value
			row, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			tests = append(tests, rowCase{fmt.Sprintf("column %d type %T", i, value), row})
		}
	}
	for _, timestamp := range []string{"", "invalid", "1630048897897.5", "9223372036854775808"} {
		fields := append([]interface{}{}, valid...)
		fields[0] = timestamp
		row, _ := json.Marshal(fields)
		tests = append(tests, rowCase{"timestamp=" + timestamp, row})
	}
	for _, confirm := range []string{"", "2", "true", "1.0"} {
		fields := append([]interface{}{}, valid...)
		fields[8] = confirm
		row, _ := json.Marshal(fields)
		tests = append(tests, rowCase{"confirm=" + confirm, row})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candle := market.Candle{O: "previous open", VolCcyQuote: "previous quote", Confirm: "1"}
			before := candle
			if err := candle.UnmarshalJSON(tt.data); err == nil {
				t.Fatal("invalid row was accepted")
			}
			if candle != before {
				t.Errorf("failed decoding changed the receiver: %+v", candle)
			}
		})
	}
}
