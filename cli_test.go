package main

import (
	"strings"
	"testing"
)

func TestExecuteVersionCommands(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}, {"-v"}} {
		var out, errOut strings.Builder
		if code := execute(args, &out, &errOut); code != 0 {
			t.Fatalf("execute(%q) code = %d, stderr = %q", args, code, errOut.String())
		}
		if !strings.Contains(out.String(), "ISKrona "+version) {
			t.Fatalf("execute(%q) output = %q", args, out.String())
		}
		if strings.Contains(out.String(), "EVE Online margin price relay") {
			t.Fatalf("execute(%q) unexpectedly printed the removed banner: %q", args, out.String())
		}
	}
}

func TestExecuteInspectRequiresPath(t *testing.T) {
	var out, errOut strings.Builder
	if code := execute([]string{"inspect"}, &out, &errOut); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if got := errOut.String(); !strings.Contains(got, "inspect requires --path FILE") {
		t.Fatalf("stderr = %q", got)
	}
}

func TestExecuteHelpDoesNotPrintBanner(t *testing.T) {
	var out, errOut strings.Builder
	if code := execute([]string{"tui", "--help"}, &out, &errOut); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errOut.String())
	}
	if strings.Contains(out.String(), "███████") {
		t.Fatalf("help unexpectedly includes banner: %q", out.String())
	}
	if !strings.Contains(out.String(), "--no-color") {
		t.Fatalf("help does not show tui flags: %q", out.String())
	}
	if strings.Contains(out.String(), "--tui") {
		t.Fatalf("help unexpectedly shows removed TUI flag: %q", out.String())
	}
}

func TestExecuteRejectsInvalidCommandArguments(t *testing.T) {
	var out, errOut strings.Builder
	if code := execute([]string{"version", "extra"}, &out, &errOut); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if errOut.Len() == 0 {
		t.Fatal("expected validation error")
	}
}

func TestWatchCommandIsHiddenDeprecatedAlias(t *testing.T) {
	cmd := newWatchCmd()
	if !cmd.Hidden || cmd.Deprecated == "" {
		t.Fatalf("watch alias hidden = %v, deprecated = %q", cmd.Hidden, cmd.Deprecated)
	}
	if err := cmd.Flags().Parse([]string{"--path", "logs", "--jumps", "0", "--copy", "buy", "--no-color"}); err != nil {
		t.Fatal(err)
	}
	if cmd.Flags().Lookup("path") == nil || cmd.Flags().Lookup("jumps") == nil || cmd.Flags().Lookup("copy") == nil || cmd.Flags().Lookup("no-color") == nil {
		t.Fatal("watch alias does not expose the TUI flags")
	}
}

func TestTUIOverridesOnlyUseExplicitFlags(t *testing.T) {
	cmd := newTUICmd()
	if err := cmd.Flags().Parse([]string{"--path", "logs", "--jumps", "0", "--copy", "buy", "--no-color"}); err != nil {
		t.Fatal(err)
	}
	o := tuiOverrides(cmd, "logs", 0, "buy", true)
	if o.Path == nil || *o.Path != "logs" || o.Jumps == nil || *o.Jumps != 0 || o.CopyMode == nil || *o.CopyMode != "buy" || !o.NoColor {
		t.Fatalf("overrides = %#v", o)
	}
}
