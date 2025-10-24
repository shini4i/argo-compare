package command

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/shini4i/argo-compare/internal/app"
	"github.com/shini4i/argo-compare/internal/helpers"
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
	flags := loadBranchDefaults()

	cmd := &cobra.Command{
		Use:   "branch <name>",
		Short: "Compare Applications against a target branch",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dropCache() {
				return nil
			}

			params := flags
			params.applyFullOutput()

			configOptions, err := params.configOptions(opts, debug())
			if err != nil {
				return err
			}

			cfg, err := app.NewConfig(args[0], configOptions...)
			if err != nil {
				return err
			}

			if opts.RunApp == nil {
				return errors.New("no run handler provided")
			}

			return opts.RunApp(cfg)
		},
	}

	cmd.Flags().StringVarP(&flags.file, "file", "f", "", "Compare a single file")
	cmd.Flags().StringSliceVarP(&flags.ignore, "ignore", "i", nil, "Ignore specific files (can be set multiple times)")
	cmd.Flags().BoolVar(&flags.preserveHelmLabels, "preserve-helm-labels", false, "Preserve Helm labels during comparison")
	cmd.Flags().BoolVar(&flags.printAdded, "print-added-manifests", false, "Print added manifests")
	cmd.Flags().BoolVar(&flags.printRemoved, "print-removed-manifests", false, "Print removed manifests")
	cmd.Flags().BoolVar(&flags.fullOutput, "full-output", false, "Print all changed, added, and removed manifests")
	cmd.Flags().StringVar(&flags.commentProvider, "comment-provider", flags.commentProvider, "Post diff comment using provider (gitlab)")
	cmd.Flags().StringVar(&flags.gitlabURL, "gitlab-url", flags.gitlabURL, "GitLab base URL (e.g., https://gitlab.com)")
	cmd.Flags().StringVar(&flags.gitlabToken, "gitlab-token", flags.gitlabToken, "GitLab personal access token")
	cmd.Flags().StringVar(&flags.gitlabProjectID, "gitlab-project-id", flags.gitlabProjectID, "GitLab project ID")
	cmd.Flags().IntVar(&flags.gitlabMergeIID, "gitlab-merge-request-iid", flags.gitlabMergeIID, "GitLab merge request IID")

	return cmd
}

type branchFlags struct {
	file               string
	ignore             []string
	preserveHelmLabels bool
	printAdded         bool
	printRemoved       bool
	fullOutput         bool
	commentProvider    string
	gitlabURL          string
	gitlabToken        string
	gitlabProjectID    string
	gitlabMergeIID     int
}

func loadBranchDefaults() branchFlags {
	defaults := branchFlags{}

	provider := helpers.GetEnv("ARGO_COMPARE_COMMENT_PROVIDER", "")
	if provider == "" && helpers.GetEnv("GITLAB_CI", "") != "" && helpers.GetEnv("CI_MERGE_REQUEST_IID", "") != "" {
		provider = string(app.CommentProviderGitLab)
	}
	defaults.commentProvider = provider

	url := helpers.GetEnv("ARGO_COMPARE_GITLAB_URL", "")
	if url == "" {
		url = helpers.GetEnv("CI_SERVER_URL", "")
	}
	defaults.gitlabURL = url

	token := helpers.GetEnv("ARGO_COMPARE_GITLAB_TOKEN", "")
	if token == "" {
		token = helpers.GetEnv("CI_JOB_TOKEN", "")
	}
	defaults.gitlabToken = token

	projectID := helpers.GetEnv("ARGO_COMPARE_GITLAB_PROJECT_ID", "")
	if projectID == "" {
		projectID = helpers.GetEnv("CI_PROJECT_ID", "")
	}
	defaults.gitlabProjectID = projectID

	mergeIID := helpers.GetEnv("ARGO_COMPARE_GITLAB_MR_IID", "")
	if mergeIID == "" {
		mergeIID = helpers.GetEnv("CI_MERGE_REQUEST_IID", "")
	}
	if mergeIID != "" {
		if parsed, err := strconv.Atoi(mergeIID); err == nil {
			defaults.gitlabMergeIID = parsed
		}
	}

	return defaults
}

func (b *branchFlags) applyFullOutput() {
	if b.fullOutput {
		b.printAdded = true
		b.printRemoved = true
	}
}

func (b branchFlags) configOptions(opts Options, debugEnabled bool) ([]app.ConfigOption, error) {
	options := []app.ConfigOption{
		app.WithFileToCompare(b.file),
		app.WithFilesToIgnore(b.ignore),
		app.WithPreserveHelmLabels(b.preserveHelmLabels),
		app.WithPrintAdded(b.printAdded),
		app.WithPrintRemoved(b.printRemoved),
		app.WithCacheDir(opts.CacheDir),
		app.WithTempDirBase(opts.TempDirBase),
		app.WithExternalDiffTool(opts.ExternalDiffTool),
		app.WithDebug(debugEnabled),
		app.WithVersion(opts.Version),
	}

	commentOption, err := b.commentOption()
	if err != nil {
		return nil, err
	}
	if commentOption != nil {
		options = append(options, commentOption)
	}

	return options, nil
}

func (b branchFlags) commentOption() (app.ConfigOption, error) {
	provider := strings.ToLower(strings.TrimSpace(b.commentProvider))
	switch provider {
	case "":
		return nil, nil
	case string(app.CommentProviderGitLab):
		return app.WithCommentConfig(app.CommentConfig{
			Provider: app.CommentProviderGitLab,
			GitLab: app.GitLabCommentConfig{
				BaseURL:         b.gitlabURL,
				Token:           b.gitlabToken,
				ProjectID:       b.gitlabProjectID,
				MergeRequestIID: b.gitlabMergeIID,
			},
		}), nil
	default:
		return nil, fmt.Errorf("unsupported comment provider %q", b.commentProvider)
	}
}
