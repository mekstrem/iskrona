package main

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"strconv"
	"strings"
)

// Order is the subset of an EVE market export row needed for pricing.
// PriceCents is an integer number of ISK cents; this avoids floating point
// rounding when applying EVE's tick rules.
type Order struct {
	PriceCents int64
	Bid        bool
	Jumps      int
}

// MarketSummary contains the best eligible orders and the prices to copy for
// each side. A nil order means that side had no eligible orders.
type MarketSummary struct {
	BestBuy   *Order
	BestSell  *Order
	BuyCents  int64
	SellCents int64
}

// ParseMarketCSV parses an EVE market export. Columns are located by header
// name, so additional columns and changes in column order are harmless.
func ParseMarketCSV(r io.Reader) ([]Order, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1 // EVE exports have a trailing empty column.
	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("read market export header: %w", err)
	}
	indexes := make(map[string]int, len(header))
	for i, h := range header {
		indexes[strings.ToLower(strings.TrimSpace(h))] = i
	}
	priceAt, pok := indexes["price"]
	bidAt, bok := indexes["bid"]
	jumpsAt, jok := indexes["jumps"]
	if !pok || !bok || !jok {
		return nil, errors.New("market export must contain price, bid, and jumps columns")
	}
	maxAt := priceAt
	if bidAt > maxAt {
		maxAt = bidAt
	}
	if jumpsAt > maxAt {
		maxAt = jumpsAt
	}
	var orders []Order
	for row := 2; ; row++ {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read market export row %d: %w", row, err)
		}
		if len(rec) <= maxAt {
			return nil, fmt.Errorf("market export row %d is missing required fields", row)
		}
		price, err := parseCents(rec[priceAt])
		if err != nil {
			return nil, fmt.Errorf("row %d price: %w", row, err)
		}
		bid, err := parseBool(rec[bidAt])
		if err != nil {
			return nil, fmt.Errorf("row %d bid: %w", row, err)
		}
		jumps, err := strconv.Atoi(strings.TrimSpace(rec[jumpsAt]))
		if err != nil || jumps < 0 {
			return nil, fmt.Errorf("row %d jumps is not a nonnegative integer", row)
		}
		orders = append(orders, Order{PriceCents: price, Bid: bid, Jumps: jumps})
	}
	return orders, nil
}

func parseBool(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes":
		return true, nil
	case "false", "0", "no":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean %q", s)
	}
}

// parseCents parses a nonnegative decimal ISK amount exactly. More than two
// fractional digits are accepted only when the extra digits are zero.
func parseCents(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "-") {
		return 0, errors.New("price must be a nonnegative decimal")
	}
	parts := strings.Split(s, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, fmt.Errorf("invalid decimal %q", s)
	}
	whole := new(big.Int)
	if _, ok := whole.SetString(parts[0], 10); !ok {
		return 0, fmt.Errorf("invalid decimal %q", s)
	}
	frac := ""
	if len(parts) == 2 {
		frac = parts[1]
	}
	for _, c := range frac {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("invalid decimal %q", s)
		}
	}
	if len(frac) > 2 && strings.Trim(frac[2:], "0") != "" {
		return 0, fmt.Errorf("price has more than two decimal places")
	}
	for len(frac) < 2 {
		frac += "0"
	}
	minor := new(big.Int)
	if frac != "" {
		if _, ok := minor.SetString(frac[:2], 10); !ok {
			return 0, fmt.Errorf("invalid decimal %q", s)
		}
	}
	whole.Mul(whole, big.NewInt(100)).Add(whole, minor)
	if !whole.IsInt64() {
		return 0, errors.New("price is too large")
	}
	price := whole.Int64()
	if RaiseTick(price) == 0 {
		return 0, errors.New("price is too large to raise")
	}
	return price, nil
}

// AnalyzeMarket selects the highest eligible buy and lowest eligible sell.
// The jump limit is inclusive.
func AnalyzeMarket(orders []Order, maxJumps int) MarketSummary {
	var out MarketSummary
	for i := range orders {
		o := orders[i]
		if o.Jumps > maxJumps {
			continue
		}
		if o.Bid {
			if out.BestBuy == nil || o.PriceCents > out.BestBuy.PriceCents {
				x := o
				out.BestBuy = &x
			}
		} else if out.BestSell == nil || o.PriceCents < out.BestSell.PriceCents {
			x := o
			out.BestSell = &x
		}
	}
	if out.BestBuy != nil {
		out.BuyCents = RaiseTick(out.BestBuy.PriceCents)
	}
	if out.BestSell != nil {
		out.SellCents = LowerTick(out.BestSell.PriceCents)
	}
	return out
}

// RaiseTick returns the nearest valid price strictly above p, or zero if it cannot fit in int64.
func RaiseTick(p int64) int64 {
	t := tickForRaise(p)
	base := p - p%t
	if base > math.MaxInt64-t {
		return 0
	}
	return base + t
}

// LowerTick returns the nearest valid price strictly below p. At an exact
// power of ten, the lower side uses the finer tick of the lower price band.
func LowerTick(p int64) int64 {
	if p <= 1 {
		return 1
	} // prices are represented in cents; 0.01 is the floor.
	p--
	return p - p%tickForRaise(p)
}

func tickForRaise(p int64) int64 {
	if p < 1 {
		return 1
	}
	t := int64(1)
	// Prices are cents. At 100 ISK (10,000 cents) the four-significant-
	// figure tick becomes 0.10 ISK (10 cents), then grows by ten each decade.
	for p >= 10000 {
		p /= 10
		t *= 10
	}
	return t
}

// CalculateMargin returns percentage margin after buy and sell fees.
func CalculateMargin(buyCents, sellCents int64, buyBroker, brokerFee, salesTax float64) float64 {
	if buyCents <= 0 || sellCents <= 0 {
		return 0
	}
	return 100 * (1 - (float64(buyCents)*(1+buyBroker))/(float64(sellCents)*(1-brokerFee-salesTax)))
}

// FormatCents formats an integer ISK amount with exactly two decimal places.
func FormatCents(cents int64) string {
	neg := cents < 0
	if neg {
		cents = -cents
	}
	s := fmt.Sprintf("%d.%02d", cents/100, cents%100)
	if neg {
		return "-" + s
	}
	return s
}

// FormatDisplayCents adds readable thousands separators for terminal output.
// Clipboard values should continue to use FormatCents so they remain directly
// usable in EVE's price fields.
func FormatDisplayCents(cents int64) string {
	raw := FormatCents(cents)
	sign := ""
	if strings.HasPrefix(raw, "-") {
		sign, raw = "-", raw[1:]
	}
	parts := strings.SplitN(raw, ".", 2)
	whole := parts[0]
	for i := len(whole) - 3; i > 0; i -= 3 {
		whole = whole[:i] + " " + whole[i:]
	}
	return sign + whole + "." + parts[1]
}

func FormatDisplayUnits(units int64) string {
	sign := ""
	if units < 0 {
		sign, units = "-", -units
	}
	whole := fmt.Sprintf("%d", units)
	for i := len(whole) - 3; i > 0; i -= 3 {
		whole = whole[:i] + " " + whole[i:]
	}
	return sign + whole
}
