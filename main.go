package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
)

const version = "0.2.0"

func main() { os.Exit(run(os.Args[1:])) }
func run(args []string) int {
	printBanner(os.Stdout)
	if len(args) == 0 {
		printUsage(os.Stdout)
		return 2
	}
	switch args[0] {
	case "help", "-h", "--help":
		printUsage(os.Stdout)
		return 0
	case "version", "-v", "--version":
		if len(args) != 1 {
			fmt.Fprintln(os.Stderr, "version takes no options")
			return 2
		}
		fmt.Println("ISKrona", version)
		return 0
	case "setup":
		return runSetup(args[1:])
	case "inspect":
		return runInspect(args[1:])
	case "watch":
		return runWatch(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown ISKrona command %q\n", args[0])
		printUsage(os.Stderr)
		return 2
	}
}
func runSetup(args []string) int {
	if len(args) > 0 {
		fmt.Fprintln(os.Stderr, "setup takes no options")
		return 2
	}
	p, e := ConfigPath()
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		return 1
	}
	c, e := LoadConfig(p)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		return 1
	}
	fmt.Printf("ISKrona settings created at %s\nMarket folder: %s\n", p, c.Market.Marketlogs)
	return 0
}
func runInspect(args []string) int {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	path := fs.String("path", "", "market export file")
	if e := fs.Parse(args); e != nil {
		return 2
	}
	if *path == "" {
		fmt.Fprintln(os.Stderr, "inspect requires --path FILE")
		return 2
	}
	f, e := os.Open(*path)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		return 1
	}
	defer f.Close()
	o, e := ParseMarketCSV(f)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		return 1
	}
	fmt.Printf("ISKrona inspection\nOrders loaded: %d\n", len(o))
	return 0
}
func runWatch(args []string) int {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	path := fs.String("path", "", "market folder override")
	jumps := fs.Int("jumps", 0, "maximum market distance")
	copyMode := fs.String("copy", "", "copy buy or sell recommendation")
	noColor := fs.Bool("no-color", false, "disable terminal colors")
	if e := fs.Parse(args); e != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "watch accepts options only")
		return 2
	}
	p, e := ConfigPath()
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		return 1
	}
	o := CLIOverrides{NoColor: *noColor}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "path":
			o.Path = path
		case "jumps":
			o.Jumps = jumps
		case "copy":
			o.CopyMode = copyMode
		}
	})
	c, e := ResolveConfig(p, o)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		return 1
	}
	ready := InitClipboard() == nil
	fmt.Printf("Watching: %s\nCopy target: %s\n\n", c.Market.Marketlogs, c.Display.CopyMode)
	r := newMarketOutputRenderer(os.Stdout)
	r.color = c.Display.Color
	h := marketFileHandler(c, ready, r)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if e = WatchDirectory(ctx, c.Market.Marketlogs, h); e != nil && !errors.Is(e, context.Canceled) {
		fmt.Fprintln(os.Stderr, e)
		return 1
	}
	return 0
}
func marketFileHandler(c Config, ready bool, r *marketOutputRenderer) func(string) error {
	return func(path string) error {
		f, e := os.Open(path)
		if e != nil {
			return nil
		}
		defer f.Close()
		orders, e := ParseMarketCSV(f)
		if e != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", filepath.Base(path), e)
			return nil
		}
		s := AnalyzeMarket(orders, c.Market.MaxJumps)
		lines := []string{"ITEM  " + strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), "", "MARKET SNAPSHOT", fmt.Sprintf("  Orders considered    %d", len(orders)), "", "PRICING"}
		if s.BestSell == nil {
			lines = append(lines, "  Sell      unavailable")
		} else {
			lines = append(lines, fmt.Sprintf("  Sell      %s ISK", FormatDisplayCents(s.SellCents)))
		}
		if s.BestBuy == nil {
			lines = append(lines, "  Buy       unavailable")
		} else {
			lines = append(lines, fmt.Sprintf("  Buy       %s ISK", FormatDisplayCents(s.BuyCents)))
		}
		if s.BestBuy == nil || s.BestSell == nil {
			lines = append(lines, "  Margin    unavailable")
		} else {
			margin := CalculateMargin(s.BuyCents, s.SellCents, c.Fees.Broker, c.Fees.SalesTax)
			marginText := fmt.Sprintf("%.2f%%", margin)
			if c.Display.Color {
				marginText = colorMargin(margin, marginText)
			}
			lines = append(lines, fmt.Sprintf("  Margin    %s", marginText))
		}
		if s.BestBuy != nil && s.BestSell != nil {
			lines = append(lines, "", "PROFIT EXAMPLES", profitHeader())
			previousUnits := int64(0)
			for _, budget := range []int64{100_000_000, 1_000_000_000, 10_000_000_000} {
				units := exampleUnits(s.BuyCents, budget)
				if units <= previousUnits {
					units = previousUnits * 2
				}
				previousUnits = units
				cost, revenue, profit := profitExample(s.BuyCents, s.SellCents, units, c.Fees.Broker, c.Fees.SalesTax)
				lines = append(lines, formatProfitRow(units, cost, revenue, profit))
			}
		}
		if ready {
			var v int64
			if c.Display.CopyMode == "buy" {
				v = s.BuyCents
			} else {
				v = s.SellCents
			}
			if v > 0 {
				value := FormatCents(v)
				if e := CopyText(value); e != nil {
					fmt.Fprintln(os.Stderr, "warning: clipboard unavailable")
				}
			}
		}
		return r.render(lines...)
	}
}

func exampleUnits(unitPrice, targetBudgetCents int64) int64 {
	if unitPrice <= 0 {
		return 1
	}
	units := (targetBudgetCents + unitPrice - 1) / unitPrice
	if units < 1 {
		return 1
	}
	// Round to a clean quantity so the examples are easy to act on.
	scale := int64(1)
	for units/scale >= 10 {
		scale *= 10
	}
	lead := (units + scale - 1) / scale
	if lead > 5 {
		lead = 10
	} else if lead > 2 {
		lead = 5
	}
	return lead * scale
}

func profitExample(buyCents, sellCents, units int64, broker, tax float64) (cost, revenue, profit int64) {
	cost = int64(math.Round(float64(buyCents*units) * (1 + broker)))
	revenue = int64(math.Round(float64(sellCents*units) * (1 - broker - tax)))
	return cost, revenue, revenue - cost
}

func formatProfitRow(units, cost, revenue, profit int64) string {
	return fmt.Sprintf("  %12s  %18s  %18s  %22s", FormatDisplayUnits(units), FormatDisplayCents(cost), FormatDisplayCents(revenue), FormatDisplayCents(profit)+" ISK")
}

func profitHeader() string {
	return fmt.Sprintf("  %12s  %18s  %18s  %22s", "Units", "Cost", "Revenue", "Net profit")
}

func colorMargin(margin float64, text string) string {
	color := "\x1b[32m"
	if margin < 10 {
		color = "\x1b[31m"
	} else if margin < 25 {
		color = "\x1b[33m"
	}
	return color + text + "\x1b[0m"
}
func printUsage(w io.Writer) {
	fmt.Fprintln(w, "iskrona — EVE market signal console\n\nCommands:\n  watch                 monitor exports and show recommendations\n  setup                 create or display ISKrona settings\n  inspect --path FILE   inspect one market export\n  version               print ISKrona version\n  help                  show this guide\n\nwatch options:\n  --path DIR            market folder override\n  --jumps N             maximum market distance\n  --copy buy|sell       recommendation to copy\n  --no-color            plain terminal output")
}

func printBanner(w io.Writer) {
	fmt.Fprintln(w, "  ██╗███████╗██╗  ██╗██████╗  ██████╗ ███╗   ██╗ █████╗")
	fmt.Fprintln(w, "  ██║██╔════╝██║ ██╔╝██╔══██╗██╔═══██╗████╗  ██║██╔══██╗")
	fmt.Fprintln(w, "  ██║███████╗█████╔╝ ██████╔╝██║   ██║██╔██╗ ██║███████║")
	fmt.Fprintln(w, "  ██║╚════██║██╔═██╗ ██╔══██╗██║   ██║██║╚██╗██║██╔══██║")
	fmt.Fprintln(w, "  ██║███████║██║  ██╗██║  ██║╚██████╔╝██║ ╚████║██║  ██║")
	fmt.Fprintln(w, "  ╚═╝╚══════╝╚═╝  ╚═╝╚═╝  ╚═╝ ╚═════╝ ╚═╝  ╚═══╝╚═╝  ╚═╝")
	fmt.Fprintln(w, "  EVE Online margin price relay")
	fmt.Fprintln(w)
}
