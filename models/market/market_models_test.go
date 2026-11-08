package market_test

import (
	"encoding/json"
	"fmt"
	"testing"

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
