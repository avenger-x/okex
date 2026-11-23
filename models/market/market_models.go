package market

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/amir-the-h/okex"
)

type (
	Ticker struct {
		InstID    string              `json:"instId"`
		Last      okex.JSONFloat64    `json:"last"`
		LastSz    okex.JSONFloat64    `json:"lastSz"`
		AskPx     string              `json:"askPx"`
		AskSz     string              `json:"askSz"`
		BidPx     string              `json:"bidPx"`
		BidSz     string              `json:"bidSz"`
		Open24h   okex.JSONFloat64    `json:"open24h"`
		High24h   okex.JSONFloat64    `json:"high24h"`
		Low24h    okex.JSONFloat64    `json:"low24h"`
		VolCcy24h okex.JSONFloat64    `json:"volCcy24h"`
		Vol24h    okex.JSONFloat64    `json:"vol24h"`
		SodUtc0   okex.JSONFloat64    `json:"sodUtc0"`
		SodUtc8   okex.JSONFloat64    `json:"sodUtc8"`
		InstType  okex.InstrumentType `json:"instType"`
		TS        okex.JSONTime       `json:"ts"`
	}
	IndexTicker struct {
		InstID  string           `json:"instId"`
		IdxPx   okex.JSONFloat64 `json:"idxPx"`
		High24h okex.JSONFloat64 `json:"high24h"`
		Low24h  okex.JSONFloat64 `json:"low24h"`
		Open24h okex.JSONFloat64 `json:"open24h"`
		SodUtc0 okex.JSONFloat64 `json:"sodUtc0"`
		SodUtc8 okex.JSONFloat64 `json:"sodUtc8"`
		TS      okex.JSONTime    `json:"ts"`
	}
	OrderBook struct {
		Asks []*OrderBookEntity `json:"asks"`
		Bids []*OrderBookEntity `json:"bids"`
		TS   okex.JSONTime      `json:"ts"`
	}
	OrderBookWs struct {
		Asks      []*OrderBookEntity `json:"asks"`
		Bids      []*OrderBookEntity `json:"bids"`
		Checksum  int                `json:"checksum"`
		SeqID     int64              `json:"seqId"`
		PrevSeqID int64              `json:"prevSeqId"`
		TS        okex.JSONTime      `json:"ts"`
	}
	OrderBookEntity struct {
		DepthPrice      string
		Size            string
		LiquidatedOrder int
		OrderNumbers    int
	}
	Candle struct {
		O           string
		H           string
		L           string
		C           string
		Vol         string
		VolCcy      string
		VolCcyQuote string
		Confirm     string
		TS          okex.JSONTime
	}
	IndexCandle struct {
		O  float64
		H  float64
		L  float64
		C  float64
		TS okex.JSONTime
	}
	Trade struct {
		InstID  string           `json:"instId"`
		TradeID okex.JSONFloat64 `json:"tradeId"`
		Px      okex.JSONFloat64 `json:"px"`
		Sz      okex.JSONFloat64 `json:"sz"`
		Side    okex.TradeSide   `json:"side"`
		TS      okex.JSONTime    `json:"ts"`
	}
	TotalVolume24H struct {
		VolUsd okex.JSONFloat64 `json:"volUsd"`
		VolCny okex.JSONFloat64 `json:"volCny"`
		TS     okex.JSONTime    `json:"ts"`
	}
	IndexComponent struct {
		Index      string           `json:"index"`
		Last       okex.JSONFloat64 `json:"last"`
		Components []*Component     `json:"components"`
		TS         okex.JSONTime    `json:"ts"`
	}
	Component struct {
		Exch   string           `json:"exch"`
		Symbol string           `json:"symbol"`
		SymPx  okex.JSONFloat64 `json:"symPx"`
		Wgt    okex.JSONFloat64 `json:"wgt"`
		CnvPx  okex.JSONFloat64 `json:"cnvPx"`
	}
)

func (o *OrderBookEntity) UnmarshalJSON(buf []byte) error {
	var (
		dp, s, lo, on string
		err           error
	)
	tmp := []interface{}{&dp, &s, &lo, &on}
	wantLen := len(tmp)
	if err := json.Unmarshal(buf, &tmp); err != nil {
		return err
	}

	if g, e := len(tmp), wantLen; g != e {
		return fmt.Errorf("wrong number of fields in OrderBookEntity: %d != %d", g, e)
	}
	o.DepthPrice = dp
	o.Size = s
	o.LiquidatedOrder, err = strconv.Atoi(lo)
	if err != nil {
		return err
	}
	o.OrderNumbers, err = strconv.Atoi(on)
	if err != nil {
		return err
	}

	return nil
}

func (c *Candle) UnmarshalJSON(buf []byte) error {
	// Pointers distinguish JSON null from an empty string, while decoding into
	// strings rejects numeric fields instead of silently converting decimals.
	var fields []*string
	if err := json.Unmarshal(buf, &fields); err != nil {
		return err
	}
	if len(fields) != 9 {
		return fmt.Errorf("wrong number of fields in Candle: %d != 9", len(fields))
	}
	for i, field := range fields {
		if field == nil {
			return fmt.Errorf("null field in Candle at column %d", i)
		}
	}
	timestamp, err := strconv.ParseInt(*fields[0], 10, 64)
	if err != nil {
		return fmt.Errorf("invalid Candle timestamp: %w", err)
	}
	if *fields[8] != "0" && *fields[8] != "1" {
		return fmt.Errorf("invalid Candle confirm: %q", *fields[8])
	}
	*c = Candle{
		TS:          okex.JSONTime(time.UnixMilli(timestamp)),
		O:           *fields[1],
		H:           *fields[2],
		L:           *fields[3],
		C:           *fields[4],
		Vol:         *fields[5],
		VolCcy:      *fields[6],
		VolCcyQuote: *fields[7],
		Confirm:     *fields[8],
	}
	return nil
}

func (c *IndexCandle) UnmarshalJSON(buf []byte) error {
	var (
		o, h, l, cl, ts string
		err             error
	)
	tmp := []interface{}{&ts, &o, &h, &l, &cl}
	wantLen := len(tmp)
	if err := json.Unmarshal(buf, &tmp); err != nil {
		return err
	}

	if g, e := len(tmp), wantLen; g != e {
		return fmt.Errorf("wrong number of fields in Candle: %d != %d", g, e)
	}

	timestamp, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return err
	}
	*(*time.Time)(&c.TS) = time.UnixMilli(timestamp)

	c.O, err = strconv.ParseFloat(o, 64)
	if err != nil {
		return err
	}

	c.H, err = strconv.ParseFloat(h, 64)
	if err != nil {
		return err
	}

	c.L, err = strconv.ParseFloat(l, 64)
	if err != nil {
		return err
	}

	c.C, err = strconv.ParseFloat(cl, 64)
	if err != nil {
		return err
	}

	return nil
}
