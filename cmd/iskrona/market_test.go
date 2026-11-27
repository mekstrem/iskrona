package main

import (
	"math"
	"slices"
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
	for _, order := range orders {
		if order.Volume != 0 {
			t.Fatalf("order without volume column has volume %d", order.Volume)
		}
	}
	if s.HasBuyDepth || s.HasSellDepth {
		t.Fatalf("export without volume column has depth: %#v", s)
	}
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
		{"short volume row", "price,bid,jumps,volRemaining\n1.00,True,0\n", "row 2 is missing volRemaining"},
		{"invalid volume", "price,bid,jumps,volRemaining\n1.00,True,0,1.5\n", "row 2 volRemaining"},
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
	data := " PRICE , VOLREMAINING , BID , JUMPS ,extra\n1,1.0,yes,0,x\n2.5,12.000,0,1,x\n3.1200,400,1,2,x\n4.00,0,no,3,x\n"
	orders, err := ParseMarketCSV(strings.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	want := []Order{{PriceCents: 100, Bid: true, Volume: 1}, {PriceCents: 250, Jumps: 1, Volume: 12}, {PriceCents: 312, Bid: true, Jumps: 2, Volume: 400}, {PriceCents: 400, Jumps: 3}}
	if len(orders) != len(want) {
		t.Fatalf("orders = %#v, want %#v", orders, want)
	}
	for i := range want {
		if orders[i] != want[i] {
			t.Errorf("order %d = %#v, want %#v", i, orders[i], want[i])
		}
	}
}

func TestParseUnits(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  int64
		ok    bool
	}{
		{"1234", 1234, true}, {"1.0", 1, true}, {"12.000", 12, true},
		{"1.5", 0, false}, {"-1", 0, false}, {"", 0, false}, {"abc", 0, false},
	} {
		got, err := parseUnits(tc.input)
		if got != tc.want || (err == nil) != tc.ok {
			t.Errorf("parseUnits(%q) = %d, %v; want %d, ok=%t", tc.input, got, err, tc.want, tc.ok)
		}
	}
}

func TestDepthPrice(t *testing.T) {
	// The 500-unit sample is the best 5% of each 10,000-unit side.
	orders := []Order{
		{PriceCents: 600, Volume: 9500}, {PriceCents: 300, Bid: true, Volume: 9500},
		{PriceCents: 500, Volume: 480}, {PriceCents: 500, Bid: true, Volume: 480},
	}
	for range 20 {
		orders = append(orders, Order{PriceCents: 400, Volume: 1}, Order{PriceCents: 400, Bid: true, Volume: 1})
	}
	original := slices.Clone(orders)
	for _, bid := range []bool{false, true} {
		if cents, volume, ok := DepthPrice(orders, 0, bid); cents != 496 || volume != 10_000 || !ok {
			t.Errorf("DepthPrice(bid=%t) = %d, %d, %t; want 496, 10000, true", bid, cents, volume, ok)
		}
	}
	got := AnalyzeMarket(orders, 0)
	if !got.HasBuyDepth || !got.HasSellDepth || got.BuyDepthCents != 497 || got.SellDepthCents != 495 ||
		got.BuyDepthVolume != 10_000 || got.SellDepthVolume != 10_000 {
		t.Fatalf("unexpected tick-adjusted depth: %#v", got)
	}
	if !slices.Equal(orders, original) {
		t.Fatal("depth calculation reordered the input")
	}
}

func TestDepthPricePartialOrderAndJumpFilter(t *testing.T) {
	orders := []Order{
		{PriceCents: 100, Volume: 1, Jumps: 1},
		{PriceCents: 200, Volume: 40},
		{PriceCents: 1, Volume: 1000, Jumps: 2},
	}
	for _, tc := range []struct {
		jumps  int
		price  int64
		volume int64
	}{
		{0, 200, 40},
		{1, 167, 41},
	} {
		if cents, volume, ok := DepthPrice(orders, tc.jumps, false); cents != tc.price || volume != tc.volume || !ok {
			t.Errorf("DepthPrice(maxJumps=%d) = %d, %d, %t; want %d, %d, true", tc.jumps, cents, volume, ok, tc.price, tc.volume)
		}
	}
}

func TestDepthPriceIgnoresOutlierOrders(t *testing.T) {
	// A 10M-unit buy at 0.01 ISK and a 10M-unit sell at 97,000,000 ISK would
	// otherwise swamp the 5% bucket.
	orders := []Order{
		{PriceCents: 179_400_000, Bid: true, Volume: 760},
		{PriceCents: 1, Bid: true, Volume: 10_000_000},
		{PriceCents: 194_000_000, Volume: 800},
		{PriceCents: 9_700_000_000, Volume: 10_000_000},
	}
	if cents, volume, ok := DepthPrice(orders, 0, true); cents != 179_400_000 || volume != 760 || !ok {
		t.Errorf("buy depth = %d, %d, %t; want 179400000, 760, true", cents, volume, ok)
	}
	if cents, volume, ok := DepthPrice(orders, 0, false); cents != 194_000_000 || volume != 800 || !ok {
		t.Errorf("sell depth = %d, %d, %t; want 194000000, 800, true", cents, volume, ok)
	}
}

func TestDepthPriceVolumeOverflow(t *testing.T) {
	orders := []Order{{PriceCents: 100, Volume: math.MaxInt64}, {PriceCents: 100, Volume: 1}}
	if cents, volume, ok := DepthPrice(orders[:1], 0, false); cents != 100 || volume != math.MaxInt64 || !ok {
		t.Errorf("depth at volume limit = %d, %d, %t; want 100, MaxInt64, true", cents, volume, ok)
	}
	if cents, volume, ok := DepthPrice(orders, 0, false); cents != 0 || volume != 0 || ok {
		t.Errorf("depth with volume overflow = %d, %d, %t; want 0, 0, false", cents, volume, ok)
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
	got := CalculateMargin(279300000, 599900000, .02, .02, .01, "npc")
	if got < 51.04 || got > 51.05 {
		t.Fatalf("margin %.4f", got)
	}
	got = CalculateMargin(10000, 20000, .005, .02, .01, "npc")
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
	cost, revenue, profit, _ := profitExample(10000, 20000, 10, 0.005, 0.02, 0.01, "npc")
	if cost != 100500 || revenue != 194000 || profit != 93500 {
		t.Fatalf("profit example = %d/%d/%d, want 100500/194000/93500", cost, revenue, profit)
	}
}

func TestBuyOrderFees(t *testing.T) {
	for _, tc := range []struct {
		name, location     string
		price, units, want int64
		rate               float64
	}{
		{"structure minimum and surcharge", "structure", 10000, 1, 20050, .005},
		{"structure screenshot", "structure", 26930000, 1, 27074650, .005},
		{"structure zero rate still charges surcharge", "structure", 26930000, 1, 27074650, 0},
		{"structure sub-surcharge rate", "structure", 26930000, 1, 27074650, .004},
		{"minimum once per order", "structure", 10000, 10, 110500, .005},
		{"structure percentage above minimum", "structure", 1000000, 1, 1020000, .02},
		{"structure percentage below minimum", "structure", 10000, 1, 20050, .02},
		{"npc percentage", "npc", 10000, 1, 10200, .02},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cost, _, _, ok := profitExample(tc.price, tc.price*3, tc.units, tc.rate, 0, 0, tc.location)
			if !ok || cost != tc.want {
				t.Fatalf("cost = %d, ok = %v; want %d", cost, ok, tc.want)
			}
		})
	}
	if got := CalculateMargin(10000, 30000, .005, 0, 0, "structure"); got < 33.16 || got > 33.17 {
		t.Fatalf("single-unit structure margin = %v", got)
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
