package main

import (
	"github.com/alecthomas/kong"
	"os"
)

var CLI struct {
	Debug     bool             `help:"Enable debug mode" short:"d"`
	DropCache Cache            `help:"Drop cache directory"`
	Version   kong.VersionFlag `help:"Show version" short:"v"`

	Branch struct {
		Name               string `arg:"" type:"string"`
		File               string `help:"Compare a single file" short:"f"`
		PreserveHelmLabels bool   `help:"Preserve Helm labels during comparison"`
	} `cmd:"" help:"target branch to compare with" type:"string"`
}

type Cache bool

func (c *Cache) BeforeApply(app *kong.Kong) error {
	err := os.RemoveAll(cacheDir)
	if err != nil {
		app.Exit(1)
	}
	app.Exit(0)
	return nil
}
