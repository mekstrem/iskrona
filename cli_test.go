package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultCommandUsesTUIConfigAndOverrides(t *testing.T) {
	doctorEnvironment(t)
	path, err := ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[market]\nmax_jumps = -1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		flags []string
		want  string
	}{
		{nil, "market.max_jumps must be nonnegative"},
		{[]string{"--jumps", "0"}, "market.marketlogs is empty"},
		{[]string{"--jumps", "0", "--path", filepath.Join(t.TempDir(), "missing")}, "market directory"},
		{[]string{"--jumps", "0", "--copy", "invalid", "--no-color"}, "display.copy_mode must be buy or sell"},
	} {
		var rootError string
		for _, prefix := range [][]string{nil, {"tui"}} {
			args := append(append([]string{}, prefix...), tc.flags...)
			var out, errOut strings.Builder
			if code := execute(args, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), tc.want) {
				t.Fatalf("execute(%q): code %d, stderr %q", args, code, errOut.String())
			}
			if prefix == nil {
				rootError = errOut.String()
			} else if errOut.String() != rootError {
				t.Fatalf("root and tui validation differ: %q != %q", rootError, errOut.String())
			}
		}
	}
}

func TestRootHelpAndArgumentValidation(t *testing.T) {
	root := newRootCmd()
	root.InitDefaultHelpCmd()
	for _, cmd := range root.Commands() {
		if cmd.Name() == "help" {
			t.Fatal("help subcommand remains registered")
		}
	}
	var out, errOut strings.Builder
	if code := execute([]string{"--help"}, &out, &errOut); code != 0 {
		t.Fatalf("help: code %d, stderr %q", code, errOut.String())
	}
	for _, flag := range []string{"--path", "--jumps", "--copy", "--no-color"} {
		if !strings.Contains(out.String(), flag) {
			t.Fatalf("root help missing %s", flag)
		}
	}
	if strings.Contains(out.String(), "help [command]") || strings.Contains(out.String(), "Help about any command") {
		t.Fatalf("help subcommand is listed: %s", out.String())
	}
	for _, args := range [][]string{{"-h"}, {"tui", "--help"}, {"doctor", "--help"}, {"login", "--help"}, {"logout", "--help"}, {"version", "--help"}} {
		out.Reset()
		errOut.Reset()
		if code := execute(args, &out, &errOut); code != 0 || !strings.Contains(out.String(), "Usage:") {
			t.Fatalf("execute(%q): code %d, stdout %q, stderr %q", args, code, out.String(), errOut.String())
		}
	}
	for _, args := range [][]string{{"help"}, {"help", "tui"}, {"unknown"}, {"--no-color", "extra"}, {"tui", "extra"}} {
		out.Reset()
		errOut.Reset()
		if code := execute(args, &out, &errOut); code != 2 || errOut.Len() == 0 {
			t.Fatalf("execute(%q): code %d, stderr %q", args, code, errOut.String())
		}
	}
}

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

func TestDoctorCLI(t *testing.T) {
	for _, args := range [][]string{{"setup"}, {"inspect"}, {"doctor", "extra"}, {"doctor", "--path", "x"}} {
		var out, errOut strings.Builder
		if code := execute(args, &out, &errOut); code != 2 {
			t.Fatalf("%v: code %d", args, code)
		}
	}
	var out, errOut strings.Builder
	if code := execute([]string{"--help"}, &out, &errOut); code != 0 {
		t.Fatal(code)
	}
	if !strings.Contains(out.String(), "doctor") || strings.Contains(out.String(), "setup") || strings.Contains(out.String(), "inspect") {
		t.Fatal(out.String())
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
