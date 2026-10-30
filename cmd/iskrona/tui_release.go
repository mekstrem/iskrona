//go:build !dev

package main

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
)

const marketRelayDevHelp = ""

func marketRelayExportSimulation(context.Context, string, <-chan struct{}, <-chan struct{}) tea.Cmd {
	return nil
}
