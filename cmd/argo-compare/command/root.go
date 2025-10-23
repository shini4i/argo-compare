package command

import (
	"errors"
	"fmt"
	"os"

	"github.com/shini4i/argo-compare/internal/app"
	"github.com/spf13/cobra"
)

// ErrCachePurged indicates the user requested cache removal and no further work should run.
var ErrCachePurged = errors.New("cache directory purged")

// Options describes the collaborators and defaults required to build the CLI.
type Options struct {
	Version          string
	CacheDir         string
	TempDirBase      string
	ExternalDiffTool string
	RunApp           func(app.Config) error
	InitLogging      func(debug bool)
}

// Execute builds and runs the Cobra command tree using the supplied options.
func Execute(opts Options, args []string) error {
	root := newRootCommand(opts)

	if args != nil {
		root.SetArgs(args)
	}

	if err := root.Execute(); err != nil {
		if errors.Is(err, ErrCachePurged) {
			return nil
		}
		return err
	}

	return nil
}

// newRootCommand builds the root Cobra command with global flags and hooks.
func newRootCommand(opts Options) *cobra.Command {
	var (
		debug     bool
		dropCache bool
	)

	root := &cobra.Command{
		Use:          "argo-compare",
		Short:        "Compare Argo CD applications between git branches",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if opts.InitLogging != nil {
				opts.InitLogging(debug)
			}

			if dropCache {
				fmt.Fprintf(cmd.OutOrStdout(), "===> Purging cache directory: %s\n", opts.CacheDir)
				if err := os.RemoveAll(opts.CacheDir); err != nil {
					return err
				}
				return ErrCachePurged
			}

			return nil
		},
	}

	root.Version = opts.Version
	root.PersistentFlags().BoolVarP(&debug, "debug", "d", false, "Enable debug mode")
	root.PersistentFlags().BoolVar(&dropCache, "drop-cache", false, "Drop cache directory and exit")

	root.AddCommand(newBranchCommand(opts, func() bool { return dropCache }, func() bool { return debug }))

	return root
}

// newBranchCommand constructs the branch subcommand responsible for manifest comparisons.
func newBranchCommand(opts Options, dropCache func() bool, debug func() bool) *cobra.Command {
	var (
		file               string
		ignore             []string
		preserveHelmLabels bool
		printAdded         bool
		printRemoved       bool
		fullOutput         bool
	)

	cmd := &cobra.Command{
		Use:   "branch <name>",
		Short: "Compare Applications against a target branch",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dropCache() {
				return nil
			}

			targetBranch := args[0]

			if fullOutput {
				printAdded = true
				printRemoved = true
			}

			cfg, err := app.NewConfig(
				targetBranch,
				app.WithFileToCompare(file),
				app.WithFilesToIgnore(ignore),
				app.WithPreserveHelmLabels(preserveHelmLabels),
				app.WithPrintAdded(printAdded),
				app.WithPrintRemoved(printRemoved),
				app.WithCacheDir(opts.CacheDir),
				app.WithTempDirBase(opts.TempDirBase),
				app.WithExternalDiffTool(opts.ExternalDiffTool),
				app.WithDebug(debug()),
				app.WithVersion(opts.Version),
			)
			if err != nil {
				return err
			}

			if opts.RunApp == nil {
				return errors.New("no run handler provided")
			}

			return opts.RunApp(cfg)
		},
	}

	cmd.Flags().StringVarP(&file, "file", "f", "", "Compare a single file")
	cmd.Flags().StringSliceVarP(&ignore, "ignore", "i", nil, "Ignore specific files (can be set multiple times)")
	cmd.Flags().BoolVar(&preserveHelmLabels, "preserve-helm-labels", false, "Preserve Helm labels during comparison")
	cmd.Flags().BoolVar(&printAdded, "print-added-manifests", false, "Print added manifests")
	cmd.Flags().BoolVar(&printRemoved, "print-removed-manifests", false, "Print removed manifests")
	cmd.Flags().BoolVar(&fullOutput, "full-output", false, "Print all changed, added, and removed manifests")

	return cmd
}
