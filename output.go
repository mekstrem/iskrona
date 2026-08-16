package main

import (
	"fmt"
	"io"
)

const clearPreviousLine = "\x1b[1A\r\x1b[2K"

// marketOutputRenderer replaces the previously rendered export block while
// leaving the watcher's status line above it intact.
type marketOutputRenderer struct {
	out          io.Writer
	previousRows int
	color        bool
}

func newMarketOutputRenderer(out io.Writer) *marketOutputRenderer {
	return &marketOutputRenderer{out: out}
}

func (r *marketOutputRenderer) render(lines ...string) error {
	if r.color {
		for i, line := range lines {
			if i == 0 || line == "MARKET SNAPSHOT" || line == "PRICING" || line == "CLIPBOARD" {
				lines[i] = "\x1b[36m" + line + "\x1b[0m"
			}
		}
	}
	for i := 0; i < r.previousRows; i++ {
		if _, err := io.WriteString(r.out, clearPreviousLine); err != nil {
			return err
		}
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(r.out, line); err != nil {
			return err
		}
	}
	r.previousRows = len(lines)
	return nil
}
