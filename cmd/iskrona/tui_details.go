package main

import (
	"fmt"
)

func (m marketRelayModel) details(styles marketRelayStyles) []string {
	lines := []string{styles.heading.Render("DETAILS  ·  Esc / q close"),
		fmt.Sprintf("FILTER: max distance %d jumps (not a route)", m.config.Market.MaxJumps)}
	if m.snapshot == nil {
		return append(lines,
			fmt.Sprintf("Buy broker: %.2f%%  ·  Sell broker: %.2f%%  ·  Sales tax: %.2f%%", m.config.Fees.BuyBroker*100, m.config.Fees.Broker*100, m.config.Fees.SalesTax*100),
			"Waiting for the first export; no recommendations yet.")
	}
	s := m.snapshot
	lines = append(lines, fmt.Sprintf("Buy broker: %.2f%%  ·  Sell broker: %.2f%%  ·  Sales tax: %.2f%%", s.buyBroker*100, s.broker*100, s.tax*100))
	if !s.hasBuy || !s.hasSell {
		return append(lines, "Trade costs need both an eligible buy and sell order.")
	}
	units := exampleUnits(s.buyCents, 100_000_000)
	cost, revenue, net, ok := profitExample(s.buyCents, s.sellCents, units, s.buyBroker, s.broker, s.tax)
	if !ok {
		return append(lines, "Trade cost example exceeds the supported amount.")
	}
	buyValue, sellValue := s.buyCents*units, s.sellCents*units
	return append(lines,
		styles.heading.Render("TRADE COSTS  ·  "+FormatDisplayUnits(units)+" units  ·  ISK"),
		fmt.Sprintf("%-24s %s", "Purchase value", FormatDisplayCents(buyValue)),
		fmt.Sprintf("%-24s %s", "Buy broker fee", FormatDisplayCents(cost-buyValue)),
		fmt.Sprintf("%-24s %s", "Total buy cost", FormatDisplayCents(cost)),
		fmt.Sprintf("%-24s %s", "Sale value", FormatDisplayCents(sellValue)),
		fmt.Sprintf("%-24s %s", "Sell broker + sales tax", FormatDisplayCents(sellValue-revenue)),
		fmt.Sprintf("%-24s %s", "Net proceeds", FormatDisplayCents(revenue)),
		fmt.Sprintf("%-24s %s", "Net profit after fees", styles.margin(float64(net)).Render(FormatDisplayCents(net)+" ISK")),
		styles.muted.Render("Example only; available volume and fills are not checked."))
}
