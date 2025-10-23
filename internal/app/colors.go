package app

import "github.com/fatih/color"

var (
	cyan   = color.New(color.FgCyan, color.Bold).SprintFunc()
	red    = color.New(color.FgRed, color.Bold).SprintFunc()
	yellow = color.New(color.FgYellow, color.Bold).SprintFunc()
)
