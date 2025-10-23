package main

import (
	"fmt"
	"os"

	"github.com/alecthomas/kong"
)

var CLI struct {
	Debug     bool             `help:"Enable debug mode" short:"d"`
	DropCache DropCache        `help:"Drop cache directory"`
	Version   kong.VersionFlag `help:"Show version" short:"v"`

	Branch struct {
		Name                  string   `arg:"" type:"string"`
		File                  string   `help:"Compare a single file" short:"f"`
		Ignore                []string `help:"Ignore a specific file" short:"i"`
		PreserveHelmLabels    bool     `help:"Preserve Helm labels during comparison"`
		PrintAddedManifests   bool     `help:"Print added manifests"`
		PrintRemovedManifests bool     `help:"Print removed manifests"`
		FullOutput            bool     `help:"Print added and removed manifests"`
	} `cmd:"" help:"target branch to compare with" type:"string"`
}

type DropCache bool

func (d *DropCache) BeforeApply(app *kong.Kong) error {
	if !bool(*d) {
		return nil
	}

	fmt.Printf("===> Purging cache directory: %s\n", cacheDir)

	if err := os.RemoveAll(cacheDir); err != nil {
		return err
	}

	if app != nil {
		app.Exit(0)
	}

	return nil
}
