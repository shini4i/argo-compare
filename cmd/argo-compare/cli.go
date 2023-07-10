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
		Name                  string `arg:"" type:"string"`
		File                  string `help:"Compare a single file" short:"f"`
		PreserveHelmLabels    bool   `help:"Preserve Helm labels during comparison"`
		PrintAddedManifests   bool   `help:"Print added manifests"`
		PrintRemovedManifests bool   `help:"Print removed manifests"`
		FullOutput            bool   `help:"Print full output"`
	} `cmd:"" help:"target branch to compare with" type:"string"`
}

var (
	targetBranch          string
	fileToCompare         string
	preserveHelmLabels    bool
	printAddedManifests   bool
	printRemovedManifests bool
)

type DropCache bool

func (d *DropCache) BeforeApply(app *kong.Kong) error {
	fmt.Printf("===> Purging cache directory: %s\n", cacheDir)

	if err := os.RemoveAll(cacheDir); err != nil {
		return err
	}

	// it is required to be able to unit test this function
	if app != nil {
		app.Exit(0)
	}

	return nil
}
