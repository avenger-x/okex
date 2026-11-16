package account

import (
	"github.com/amir-the-h/okex"
)

type (
	Balance struct {
		TotalEq     string            `json:"totalEq"`
		IsoEq       string            `json:"isoEq"`
		AdjEq       string            `json:"adjEq,omitempty"`
		OrdFroz     string            `json:"ordFroz,omitempty"`
		Imr         string            `json:"imr,omitempty"`
		Mmr         string            `json:"mmr,omitempty"`
		MgnRatio    string            `json:"mgnRatio,omitempty"`
		NotionalUsd string            `json:"notionalUsd,omitempty"`
		Details     []*BalanceDetails `json:"details,omitempty"`
		UTime       okex.JSONTime     `json:"uTime"`
	}
	BalanceDetails struct {
		Ccy           string        `json:"ccy"`
		Eq            string        `json:"eq"`
		CashBal       string        `json:"cashBal"`
		IsoEq         string        `json:"isoEq,omitempty"`
		AvailEq       string        `json:"availEq,omitempty"`
		DisEq         string        `json:"disEq"`
		AvailBal      string        `json:"availBal"`
		FrozenBal     string        `json:"frozenBal"`
		OrdFrozen     string        `json:"ordFrozen"`
		Liab          string        `json:"liab,omitempty"`
		Upl           string        `json:"upl,omitempty"`
		UplLib        string        `json:"uplLib,omitempty"`
		CrossLiab     string        `json:"crossLiab,omitempty"`
		IsoLiab       string        `json:"isoLiab,omitempty"`
		MgnRatio      string        `json:"mgnRatio,omitempty"`
		Interest      string        `json:"interest,omitempty"`
		Twap          string        `json:"twap,omitempty"`
		MaxLoan       string        `json:"maxLoan,omitempty"`
		EqUsd         string        `json:"eqUsd"`
		NotionalLever string        `json:"notionalLever,omitempty"`
		StgyEq        string        `json:"stgyEq"`
		IsoUpl        string        `json:"isoUpl,omitempty"`
		UTime         okex.JSONTime `json:"uTime"`
	}
	Position struct {
		InstID      string              `json:"instId"`
		PosCcy      string              `json:"posCcy,omitempty"`
		LiabCcy     string              `json:"liabCcy,omitempty"`
		OptVal      string              `json:"optVal,omitempty"`
		Ccy         string              `json:"ccy"`
		PosID       string              `json:"posId"`
		TradeID     string              `json:"tradeId"`
		Pos         string              `json:"pos"`
		AvailPos    string              `json:"availPos,omitempty"`
		AvgPx       string              `json:"avgPx"`
		MarkPx      string              `json:"markPx"`
		Upl         string              `json:"upl"`
		UplRatio    string              `json:"uplRatio"`
		Lever       string              `json:"lever"`
		LiqPx       string              `json:"liqPx,omitempty"`
		Imr         string              `json:"imr,omitempty"`
		Margin      string              `json:"margin,omitempty"`
		MgnRatio    string              `json:"mgnRatio"`
		Mmr         string              `json:"mmr"`
		Liab        string              `json:"liab,omitempty"`
		Interest    string              `json:"interest"`
		NotionalUsd string              `json:"notionalUsd"`
		ADL         string              `json:"adl"`
		Last        string              `json:"last"`
		DeltaBS     string              `json:"deltaBS"`
		DeltaPA     string              `json:"deltaPA"`
		GammaBS     string              `json:"gammaBS"`
		GammaPA     string              `json:"gammaPA"`
		ThetaBS     string              `json:"thetaBS"`
		ThetaPA     string              `json:"thetaPA"`
		VegaBS      string              `json:"vegaBS"`
		VegaPA      string              `json:"vegaPA"`
		PosSide     okex.PositionSide   `json:"posSide"`
		MgnMode     okex.MarginMode     `json:"mgnMode"`
		InstType    okex.InstrumentType `json:"instType"`
		CTime       okex.JSONTime       `json:"cTime"`
		UTime       okex.JSONTime       `json:"uTime"`
	}
	BalanceAndPosition struct {
		EventType okex.EventType    `json:"eventType"`
		PTime     okex.JSONTime     `json:"pTime"`
		UTime     okex.JSONTime     `json:"uTime"`
		PosData   []*Position       `json:"posData"`
		BalData   []*BalanceDetails `json:"balData"`
	}
	PositionAndAccountRisk struct {
		AdjEq   okex.JSONFloat64                     `json:"adjEq,omitempty"`
		BalData []*PositionAndAccountRiskBalanceData `json:"balData"`
		PosData []*PositionAndAccountRiskBalanceData `json:"posData"`
		TS      okex.JSONTime                        `json:"ts"`
	}
	PositionAndAccountRiskBalanceData struct {
		Ccy   string           `json:"ccy"`
		Eq    okex.JSONFloat64 `json:"eq"`
		DisEq okex.JSONFloat64 `json:"disEq"`
	}
	PositionAndAccountRiskPositionData struct {
		InstID      string              `json:"instId"`
		PosCcy      string              `json:"posCcy,omitempty"`
		Ccy         string              `json:"ccy"`
		NotionalCcy okex.JSONFloat64    `json:"notionalCcy"`
		Pos         okex.JSONFloat64    `json:"pos"`
		NotionalUsd okex.JSONFloat64    `json:"notionalUsd"`
		PosSide     okex.PositionSide   `json:"posSide"`
		InstType    okex.InstrumentType `json:"instType"`
		MgnMode     okex.MarginMode     `json:"mgnMode"`
	}
	Bill struct {
		Ccy       string              `json:"ccy"`
		InstID    string              `json:"instId"`
		Notes     string              `json:"notes"`
		BillID    string              `json:"billId"`
		OrdID     string              `json:"ordId"`
		BalChg    okex.JSONFloat64    `json:"balChg"`
		PosBalChg okex.JSONFloat64    `json:"posBalChg"`
		Bal       okex.JSONFloat64    `json:"bal"`
		PosBal    okex.JSONFloat64    `json:"posBal"`
		Sz        okex.JSONFloat64    `json:"sz"`
		Pnl       okex.JSONFloat64    `json:"pnl"`
		Fee       okex.JSONFloat64    `json:"fee"`
		From      okex.AccountType    `json:"from,string"`
		To        okex.AccountType    `json:"to,string"`
		InstType  okex.InstrumentType `json:"instType"`
		MgnMode   okex.MarginMode     `json:"MgnMode"`
		Type      okex.BillType       `json:"type,string"`
		SubType   okex.BillSubType    `json:"subType,string"`
		TS        okex.JSONTime       `json:"ts"`
	}
	Config struct {
		Level      string            `json:"level"`
		LevelTmp   string            `json:"levelTmp"`
		AcctLv     string            `json:"acctLv"`
		AutoLoan   bool              `json:"autoLoan"`
		UID        string            `json:"uid"`
		GreeksType okex.GreekType    `json:"greeksType"`
		PosMode    okex.PositionType `json:"posMode"`
	}
	PositionMode struct {
		PosMode okex.PositionType `json:"posMode"`
	}
	Leverage struct {
		InstID  string            `json:"instId"`
		Lever   okex.JSONFloat64  `json:"lever"`
		MgnMode okex.MarginMode   `json:"mgnMode"`
		PosSide okex.PositionSide `json:"posSide"`
	}
	MaxBuySellAmount struct {
		InstID  string           `json:"instId"`
		Ccy     string           `json:"ccy"`
		MaxBuy  okex.JSONFloat64 `json:"maxBuy"`
		MaxSell okex.JSONFloat64 `json:"maxSell"`
	}
	MaxAvailableTradeAmount struct {
		InstID    string           `json:"instId"`
		AvailBuy  okex.JSONFloat64 `json:"availBuy"`
		AvailSell okex.JSONFloat64 `json:"availSell"`
	}
	MarginBalanceAmount struct {
		InstID  string            `json:"instId"`
		Amt     okex.JSONFloat64  `json:"amt"`
		PosSide okex.PositionSide `json:"posSide,string"`
		Type    okex.CountAction  `json:"type,string"`
	}
	Loan struct {
		InstID  string           `json:"instId"`
		MgnCcy  string           `json:"mgnCcy"`
		Ccy     string           `json:"ccy"`
		MaxLoan okex.JSONFloat64 `json:"maxLoan"`
		MgnMode okex.MarginMode  `json:"mgnMode"`
		Side    okex.OrderSide   `json:"side,string"`
	}
	Fee struct {
		Level    string              `json:"level"`
		Taker    okex.JSONFloat64    `json:"taker"`
		Maker    okex.JSONFloat64    `json:"maker"`
		Delivery okex.JSONFloat64    `json:"delivery,omitempty"`
		Exercise okex.JSONFloat64    `json:"exercise,omitempty"`
		Category okex.FeeCategory    `json:"category,string"`
		InstType okex.InstrumentType `json:"instType"`
		TS       okex.JSONTime       `json:"ts"`
	}
	InterestAccrued struct {
		InstID       string           `json:"instId"`
		Ccy          string           `json:"ccy"`
		Interest     okex.JSONFloat64 `json:"interest"`
		InterestRate okex.JSONFloat64 `json:"interestRate"`
		Liab         okex.JSONFloat64 `json:"liab"`
		MgnMode      okex.MarginMode  `json:"mgnMode"`
		TS           okex.JSONTime    `json:"ts"`
	}
	InterestRate struct {
		Ccy          string           `json:"ccy"`
		InterestRate okex.JSONFloat64 `json:"interestRate"`
	}
	Greek struct {
		GreeksType string `json:"greeksType"`
	}
	MaxWithdrawal struct {
		Ccy   string           `json:"ccy"`
		MaxWd okex.JSONFloat64 `json:"maxWd"`
	}
)
