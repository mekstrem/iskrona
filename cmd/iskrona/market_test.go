package main

import (
	"strings"
	"testing"
)

func TestParseAndAnalyzeMarket(t *testing.T) {
	data := "foo,jumps,bid,price,\r\n" +
		"x,3,True,20.00,\r\n" +
		"x,2,False,30.00,\r\n" +
		"x,1,True,25.00,\r\n"
	orders, err := ParseMarketCSV(strings.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	s := AnalyzeMarket(orders, 2)
	if s.BestBuy == nil || s.BestBuy.PriceCents != 2500 {
		t.Fatalf("best buy: %#v", s.BestBuy)
	}
	if s.BestSell == nil || s.BestSell.PriceCents != 3000 {
		t.Fatalf("best sell: %#v", s.BestSell)
	}
	if s.BuyCents != 2501 || s.SellCents != 2999 {
		t.Fatalf("copy prices: %d %d", s.BuyCents, s.SellCents)
	}
}

func TestParseMarketCSVRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{"empty", "", "read market export header"},
		{"missing header", "price,bid\n1.00,True\n", "must contain price, bid, and jumps"},
		{"short row", "price,bid,jumps\n1.00,True\n", "row 2 is missing required fields"},
		{"malformed csv", "price,bid,jumps\n\"1.00,True,0\n", "read market export row 2"},
		{"empty price", "price,bid,jumps\n,True,0\n", "row 2 price: price must be a nonnegative decimal"},
		{"negative price", "price,bid,jumps\n-1.00,True,0\n", "row 2 price: price must be a nonnegative decimal"},
		{"leading decimal", "price,bid,jumps\n.50,True,0\n", "row 2 price: invalid decimal"},
		{"multiple decimals", "price,bid,jumps\n1.2.3,True,0\n", "row 2 price: invalid decimal"},
		{"nonzero precision", "price,bid,jumps\n1.001,True,0\n", "row 2 price: price has more than two decimal places"},
		{"fractional sign", "price,bid,jumps\n0.-1,True,0\n", "row 2 price: invalid decimal"},
		{"fractional plus", "price,bid,jumps\n0.+1,True,0\n", "row 2 price: invalid decimal"},
		{"overflow", "price,bid,jumps\n92233720368547759.00,True,0\n", "row 2 price: price is too large"},
		{"raise overflow", "price,bid,jumps\n92233720368547758.07,True,0\n", "row 2 price: price is too large to raise"},
		{"invalid bid", "price,bid,jumps\n1.00,maybe,0\n", "row 2 bid: invalid boolean"},
		{"negative jumps", "price,bid,jumps\n1.00,True,-1\n", "row 2 jumps is not a nonnegative integer"},
		{"invalid jumps", "price,bid,jumps\n1.00,True,nearby\n", "row 2 jumps is not a nonnegative integer"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseMarketCSV(strings.NewReader(tc.data))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ParseMarketCSV error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestParseMarketCSVAcceptsDocumentedVariants(t *testing.T) {
	data := " PRICE , BID , JUMPS ,extra\n1,yes,0,x\n2.5,0,1,x\n3.1200,1,2,x\n4.00,no,3,x\n"
	orders, err := ParseMarketCSV(strings.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	want := []Order{{PriceCents: 100, Bid: true, Jumps: 0}, {PriceCents: 250, Bid: false, Jumps: 1}, {PriceCents: 312, Bid: true, Jumps: 2}, {PriceCents: 400, Bid: false, Jumps: 3}}
	if len(orders) != len(want) {
		t.Fatalf("orders = %#v, want %#v", orders, want)
	}
	for i := range want {
		if orders[i] != want[i] {
			t.Errorf("order %d = %#v, want %#v", i, orders[i], want[i])
		}
	}
}

func TestTickPowerTenBoundaries(t *testing.T) {
	for _, tc := range []struct{ in, up, down int64 }{
		{100, 101, 99},          // 1.00 ISK
		{1000, 1001, 999},       // 10.00 ISK
		{10000, 10010, 9999},    // 100.00 ISK: lower band is 99.99
		{100000, 100100, 99990}, // 1000.00 ISK: lower uses 0.10 ISK
		{10001, 10010, 10000},
		{10009, 10010, 10000},
		{99999, 100000, 99990},
		{100001, 100100, 100000},
		{1, 2, 1},
		{9223372036854775807, 0, 9223000000000000000},
		{9222999999999999999, 9223000000000000000, 9222000000000000000},
	} {
		if got := RaiseTick(tc.in); got != tc.up {
			t.Errorf("raise %d: got %d want %d", tc.in, got, tc.up)
		}
		if got := LowerTick(tc.in); got != tc.down {
			t.Errorf("lower %d: got %d want %d", tc.in, got, tc.down)
		}
	}
}

func TestAnalyzeMarketOffGridPrices(t *testing.T) {
	orders, err := ParseMarketCSV(strings.NewReader("price,bid,jumps\n100.01,True,0\n1000.01,False,0\n"))
	if err != nil {
		t.Fatal(err)
	}
	s := AnalyzeMarket(orders, 0)
	if s.BuyCents != 10010 || s.SellCents != 100000 {
		t.Fatalf("copy prices: %d %d, want 10010 100000", s.BuyCents, s.SellCents)
	}
}

func TestCalculateMargin(t *testing.T) {
	got := CalculateMargin(279300000, 599900000, .02, .02, .01)
	if got < 51.04 || got > 51.05 {
		t.Fatalf("margin %.4f", got)
	}
	got = CalculateMargin(10000, 20000, .005, .02, .01)
	if got < 48.195 || got > 48.197 {
		t.Fatalf("asymmetric fee margin %.4f", got)
	}
}

func TestSampleScalePricing(t *testing.T) {
	data := "price,bid,jumps,\r\n" +
		"5999000.0,False,0,\r\n" +
		"6176000.0,False,0,\r\n" +
		"2793000.0,True,0,\r\n" +
		"2788000.0,True,0,\r\n"
	orders, err := ParseMarketCSV(strings.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	summary := AnalyzeMarket(orders, 0)
	if summary.BuyCents != 279400000 || summary.SellCents != 599800000 {
		t.Fatalf("prices = %d/%d, want 279400000/599800000", summary.BuyCents, summary.SellCents)
	}
	if got := FormatCents(summary.SellCents); got != "5998000.00" {
		t.Fatalf("formatted sell = %q", got)
	}
}

func TestFormatDisplayCents(t *testing.T) {
	for _, tc := range []struct {
		cents int64
		want  string
	}{
		{36980000, "369 800.00"},
		{123456789, "1 234 567.89"},
		{-123456, "-1 234.56"},
	} {
		if got := FormatDisplayCents(tc.cents); got != tc.want {
			t.Errorf("FormatDisplayCents(%d) = %q, want %q", tc.cents, got, tc.want)
		}
	}
}

func TestFormatDisplayUnits(t *testing.T) {
	if got := FormatDisplayUnits(50000000); got != "50 000 000" {
		t.Fatalf("FormatDisplayUnits = %q, want %q", got, "50 000 000")
	}
}

func TestProfitExampleScalesAndAppliesFees(t *testing.T) {
	cost, revenue, profit, _ := profitExample(10000, 20000, 10, 0.005, 0.02, 0.01)
	if cost != 100500 || revenue != 194000 || profit != 93500 {
		t.Fatalf("profit example = %d/%d/%d, want 100500/194000/93500", cost, revenue, profit)
	}
}

func TestExampleUnitsScaleToBudgets(t *testing.T) {
	if got := exampleUnits(1, 100_000_000); got != 100_000_000 {
		t.Fatalf("cheap-item example units = %d, want 100000000", got)
	}
	if got := exampleUnits(10_000_000, 100_000_000); got != 10 {
		t.Fatalf("expensive-item example units = %d, want 10", got)
	}
}
