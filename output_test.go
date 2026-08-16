package main

import (
	"strings"
	"testing"
)

func TestMarketOutputRendererRendersFirstBlock(t *testing.T) {
	var out strings.Builder
	renderer := newMarketOutputRenderer(&out)
	if err := renderer.render("first", "", "SELL: 10.00", ""); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "first\n\nSELL: 10.00\n\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestMarketOutputRendererReplacesPreviousBlock(t *testing.T) {
	var out strings.Builder
	renderer := newMarketOutputRenderer(&out)
	_ = renderer.render("first", "", "SELL: 10.00", "")
	if err := renderer.render("second", "", "SELL: 20.00", ""); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, clearPreviousLine+clearPreviousLine) {
		t.Fatalf("output did not clear both previous rows: %q", got)
	}
	if !strings.HasSuffix(got, "second\n\nSELL: 20.00\n\n") {
		t.Fatalf("output does not end with latest block: %q", got)
	}
}

func TestColorMarginBands(t *testing.T) {
	if got := colorMargin(5, "5.00%"); got != "\x1b[31m5.00%\x1b[0m" {
		t.Fatalf("tight margin color = %q", got)
	}
	if got := colorMargin(15, "15.00%"); got != "\x1b[33m15.00%\x1b[0m" {
		t.Fatalf("medium margin color = %q", got)
	}
	if got := colorMargin(40, "40.00%"); got != "\x1b[32m40.00%\x1b[0m" {
		t.Fatalf("wide margin color = %q", got)
	}
}
