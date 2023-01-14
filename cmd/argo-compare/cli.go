package main

import (
	"fmt"
	"github.com/alecthomas/kong"
	"os"
)

var CLI struct {
	Debug     bool             `help:"Enable debug mode" short:"d"`
	DropCache DropCache        `help:"Drop cache directory"`
	Version   kong.VersionFlag `help:"Show version" short:"v"`

	Branch struct {
		Name               string `arg:"" type:"string"`
		File               string `help:"Compare a single file" short:"f"`
		PreserveHelmLabels bool   `help:"Preserve Helm labels during comparison"`
	} `cmd:"" help:"target branch to compare with" type:"string"`
}

type DropCache bool

func (d *DropCache) BeforeApply(app *kong.Kong) error {
	fmt.Printf("===> Purging cache directory: %s\n", cacheDir)
	err := os.RemoveAll(cacheDir)
	if err != nil {
		return err
	}
	app.Exit(0)
	return nil
}
