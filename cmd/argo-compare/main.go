package main

import (
	"errors"
	"fmt"
	"github.com/alecthomas/kong"
	h "github.com/shini4i/argo-compare/internal/helpers"
	m "github.com/shini4i/argo-compare/internal/models"
	"os"
	"os/exec"
)

var (
	targetBranch       string
	fileToCompare      string
	debug              = false
	cacheDir           = fmt.Sprintf("%s/.cache/argo-compare", os.Getenv("HOME"))
	tmpDir             string
	version            = "local"
	repo               = GitRepo{}
	diffCommand        = h.GetEnv("ARGO_COMPARE_DIFF_COMMAND", "built-in")
	preserveHelmLabels bool
)

var CLI struct {
	Debug   bool             `help:"Enable debug mode" short:"d"`
	Version kong.VersionFlag `help:"Show version" short:"v"`

	Branch struct {
		Name               string `arg:"" type:"string"`
		File               string `help:"Compare a single file" short:"f"`
		PreserveHelmLabels bool   `help:"Preserve Helm labels"`
	} `cmd:"" help:"target branch to compare with" type:"string"`
}

type execContext = func(name string, arg ...string) *exec.Cmd

func printDebug(msg string) {
	if debug {
		fmt.Println(msg)
	}
}

func processFiles(fileName string, fileType string, application m.Application) error {
	printDebug(fmt.Sprintf("Processing [%s] file: [%s]", fileType, fileName))

	app := Application{File: fileName, Type: fileType, App: application}
	if fileType == "src" {
		if err := app.parse(); err != nil {
			return err
		}
	}

	if len(app.App.Spec.Source.Chart) == 0 {
		return errors.New("unsupported application configuration")
	}

	app.writeValuesYaml()
	if err := app.collectHelmChart(); err != nil {
		return err
	}

	app.extractChart()
	app.renderTemplate()

	return nil
}

func compareFiles(changedFiles []string) {
	for _, file := range changedFiles {
		var err error

		fmt.Printf("===> Processing changed application: [%s%s%s]\n", h.ColorCyan, file, h.ColorReset)

		tmpDir, err = os.MkdirTemp("/tmp", "argo-compare-*")
		if err != nil {
			fmt.Println(err)
		}

		if err = processFiles(file, "src", m.Application{}); err != nil {
			fmt.Printf("Could not process the source Application: %s%s%s\n", h.ColorRed, err, h.ColorReset)
			continue
		}

		app, err := repo.getChangedFileContent(targetBranch, file, exec.Command)
		if err != nil {
			fmt.Printf("Could not get the target Application from branch [%s%s%s]: %s%s%s\n",
				h.ColorCyan, targetBranch, h.ColorReset, h.ColorRed, err, h.ColorReset)
			continue
		}

		if err = processFiles(file, "dst", app); err != nil {
			fmt.Printf("Could not process the destination Application: %s\n", err)
			continue
		}

		comparer := Compare{}
		comparer.findFiles()
		comparer.printCompareResults()

		err = os.RemoveAll(tmpDir)
		if err != nil {
			fmt.Println(err.Error())
		}
	}
}

func main() {
	ctx := kong.Parse(&CLI,
		kong.Name("argo-compare"),
		kong.Description("Compare ArgoCD applications between git branches"),
		kong.UsageOnError(),
		kong.Vars{"version": version})

	switch ctx.Command() {
	case "branch <name>":
		targetBranch = CLI.Branch.Name
		if len(CLI.Branch.File) > 0 {
			fileToCompare = CLI.Branch.File
		}
	default:
		panic(ctx.Command())
	}

	if CLI.Debug {
		debug = true
	}

	if CLI.Branch.PreserveHelmLabels {
		preserveHelmLabels = true
	}

	fmt.Printf("===> Running argo-compare version [%s%s%s]\n", h.ColorCyan, version, h.ColorReset)

	var changedFiles []string
	var err error

	// There are valid cases when we want to compare a single file only
	if fileToCompare != "" {
		changedFiles = []string{fileToCompare}
	} else {
		if changedFiles, err = repo.getChangedFiles(exec.Command); err != nil {
			panic(err)
		}
		if len(changedFiles) == 0 {
			fmt.Println("No changed Application files found. Exiting...")
			os.Exit(0)
		}
	}

	compareFiles(changedFiles)

	if len(repo.invalidFiles) > 0 {
		fmt.Printf("===> The following yaml files are invalid and were skipped:\n")
		for _, file := range repo.invalidFiles {
			fmt.Printf("- %s%s%s\n", h.ColorRed, file, h.ColorReset)
		}
		os.Exit(1)
	}
}
