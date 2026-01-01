// Package ui provides shared user interface utilities for terminal output formatting.
package ui

import "github.com/fatih/color"

// Color functions for consistent terminal output formatting across the codebase.
var (
	Cyan   = color.New(color.FgCyan, color.Bold).SprintFunc()
	Red    = color.New(color.FgRed, color.Bold).SprintFunc()
	Yellow = color.New(color.FgYellow, color.Bold).SprintFunc()
)
