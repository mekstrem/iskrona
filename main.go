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
	root := &cobra.Command{
		Use: "iskrona", Short: "EVE market signal console", SilenceErrors: true, SilenceUsage: true, Args: cobra.NoArgs,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if showVersion {
				fmt.Fprintln(cmd.OutOrStdout(), "ISKrona", version)
				return nil
			}
			_ = cmd.Help()
			return usageError("a command is required")
		},
	}
	root.Flags().BoolVarP(&showVersion, "version", "v", false, "print ISKrona version")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError("%v", err) })
	root.AddCommand(newSetupCmd(), newInspectCmd(), newTUICmd(), newWatchCmd(), newLoginCmd(), newLogoutCmd(), newVersionCmd())
	return root
}

func newSetupCmd() *cobra.Command {
	return &cobra.Command{Use: "setup", Short: "Create or display ISKrona settings", Args: cobra.NoArgs, RunE: runSetup}
}
func runSetup(cmd *cobra.Command, _ []string) error {
	p, err := ConfigPath()
	if err != nil {
		return commandError(err)
	}
	c, err := LoadConfig(p)
	if err != nil {
		return commandError(err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "ISKrona settings created at %s\nMarket folder: %s\n", p, c.Market.Marketlogs)
	return nil
}

func newInspectCmd() *cobra.Command {
	var path string
	cmd := &cobra.Command{Use: "inspect", Short: "Inspect one market export", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if !cmd.Flags().Changed("path") || path == "" {
			return usageError("inspect requires --path FILE")
		}
		return runInspect(cmd, path)
	}}
	cmd.Flags().StringVar(&path, "path", "", "market export file")
	return cmd
}
func runInspect(cmd *cobra.Command, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return commandError(err)
	}
	defer f.Close()
	orders, err := ParseMarketCSV(f)
	if err != nil {
		return commandError(err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "ISKrona inspection\nOrders loaded: %d\n", len(orders))
	return nil
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
