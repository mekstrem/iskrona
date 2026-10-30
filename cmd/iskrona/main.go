package main

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"

	"github.com/spf13/cobra"
)

// version is set to the release tag by the release workflow. Local builds use
// the current development version.
var version = "0.2.0"

type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string                { return e.err.Error() }
func (e *exitError) Unwrap() error                { return e.err }
func usageError(format string, args ...any) error { return &exitError{2, fmt.Errorf(format, args...)} }
func commandError(err error) error                { return &exitError{1, err} }

func main()                 { os.Exit(run(os.Args[1:])) }
func run(args []string) int { return execute(args, os.Stdout, os.Stderr) }

func execute(args []string, out, errOut io.Writer) int {
	root := newRootCmd()
	root.SetArgs(args)
	root.SetOut(out)
	root.SetErr(errOut)
	if err := root.Execute(); err != nil {
		var exitErr *exitError
		if errors.As(err, &exitErr) {
			fmt.Fprintln(errOut, exitErr.err)
			return exitErr.code
		}
		fmt.Fprintln(errOut, err)
		return 2
	}
	return 0
}

func newRootCmd() *cobra.Command {
	var showVersion bool
	root := newMarketRelayCmd("iskrona", "EVE market signal console")
	root.SilenceErrors, root.SilenceUsage = true, true
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetHelpCommand(&cobra.Command{Hidden: true})
	runRelay := root.RunE
	root.RunE = func(cmd *cobra.Command, args []string) error {
		if showVersion {
			fmt.Fprintln(cmd.OutOrStdout(), "ISKrona", version)
			return nil
		}
		return runRelay(cmd, args)
	}
	root.Flags().BoolVarP(&showVersion, "version", "v", false, "print ISKrona version")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError("%v", err) })
	root.AddCommand(newDoctorCmd(), newTUICmd(), newWatchCmd(), newLoginCmd(), newLogoutCmd(), newVersionCmd())
	return root
}

func newTUICmd() *cobra.Command {
	return newMarketRelayCmd("tui", "Open Market Relay and monitor exports")
}

func newWatchCmd() *cobra.Command {
	cmd := newMarketRelayCmd("watch", "Open Market Relay and monitor exports")
	cmd.Hidden = true
	cmd.Deprecated = "use tui instead"
	return cmd
}

func newMarketRelayCmd(use, short string) *cobra.Command {
	var path, copyMode string
	var jumps int
	var noColor bool
	cmd := &cobra.Command{Use: use, Short: short, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		o := tuiOverrides(cmd, path, jumps, copyMode, noColor)
		return runTUI(cmd, o)
	}}
	cmd.Flags().StringVar(&path, "path", "", "market folder override")
	cmd.Flags().IntVar(&jumps, "jumps", 0, "maximum market distance")
	cmd.Flags().StringVar(&copyMode, "copy", "", "copy buy or sell recommendation")
	cmd.Flags().BoolVar(&noColor, "no-color", false, "disable terminal colors")
	return cmd
}

func tuiOverrides(cmd *cobra.Command, path string, jumps int, copyMode string, noColor bool) CLIOverrides {
	o := CLIOverrides{NoColor: noColor}
	if cmd.Flags().Changed("path") {
		o.Path = &path
	}
	if cmd.Flags().Changed("jumps") {
		o.Jumps = &jumps
	}
	if cmd.Flags().Changed("copy") {
		o.CopyMode = &copyMode
	}
	return o
}

func runTUI(cmd *cobra.Command, overrides CLIOverrides) error {
	p, err := ConfigPath()
	if err != nil {
		return commandError(err)
	}
	c, err := ResolveConfig(p, overrides)
	if err != nil {
		return commandError(err)
	}
	ready := InitClipboard() == nil
	if err := runMarketRelay(p, c, ready); err != nil {
		return commandError(err)
	}
	return nil
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{Use: "version", Short: "Print ISKrona version", Args: cobra.NoArgs, Run: func(cmd *cobra.Command, _ []string) { fmt.Fprintln(cmd.OutOrStdout(), "ISKrona", version) }}
}

func exampleUnits(unitPrice, targetBudgetCents int64) int64 {
	if unitPrice <= 0 {
		return 1
	}
	units := targetBudgetCents / unitPrice
	if targetBudgetCents%unitPrice > 0 {
		units++
	}
	if units < 1 {
		return 1
	}
	// Round to a clean quantity so the examples are easy to act on.
	scale := int64(1)
	for units/scale >= 10 {
		scale *= 10
	}
	lead := units / scale
	if units%scale > 0 {
		lead++
	}
	if lead > 5 {
		lead = 10
	} else if lead > 2 {
		lead = 5
	}
	if lead > math.MaxInt64/scale {
		return 0
	}
	return lead * scale
}
func profitExample(buyCents, sellCents, units int64, buyBroker, broker, tax float64) (cost, revenue, profit int64, ok bool) {
	if units <= 0 || buyCents <= 0 || sellCents <= 0 || buyCents > math.MaxInt64/units || sellCents > math.MaxInt64/units {
		return 0, 0, 0, false
	}
	buyValue, sellValue := buyCents*units, sellCents*units
	costFloat := math.Round(float64(buyValue) * (1 + buyBroker))
	revenueFloat := math.Round(float64(sellValue) * (1 - broker - tax))
	if math.IsNaN(costFloat) || math.IsNaN(revenueFloat) || costFloat < 0 || revenueFloat < 0 || costFloat >= 0x1p63 || revenueFloat >= 0x1p63 {
		return 0, 0, 0, false
	}
	cost, revenue = int64(costFloat), int64(revenueFloat)
	if cost < buyValue || revenue > sellValue {
		return 0, 0, 0, false
	}
	return cost, revenue, revenue - cost, true
}
