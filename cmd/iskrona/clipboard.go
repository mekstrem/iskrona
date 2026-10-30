package main

import (
	"context"

	"golang.design/x/clipboard"
)

// InitClipboard initializes the platform clipboard backend. A failure is
// returned to the caller so the CLI can continue with printed prices.
func InitClipboard() error { return clipboard.Init() }

// CopyText writes UTF-8 text to the system clipboard.
func CopyText(value string) error {
	_, err := clipboard.Write(context.Background(), clipboard.FmtText, []byte(value))
	return err
}
