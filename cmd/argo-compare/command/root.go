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
	var (
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
	)

	defaultCommentProvider := helpers.GetEnv("ARGO_COMPARE_COMMENT_PROVIDER", "")
	if defaultCommentProvider == "" && helpers.GetEnv("GITLAB_CI", "") != "" && helpers.GetEnv("CI_MERGE_REQUEST_IID", "") != "" {
		defaultCommentProvider = string(app.CommentProviderGitLab)
	}
	commentProvider = defaultCommentProvider

	defaultGitlabURL := helpers.GetEnv("ARGO_COMPARE_GITLAB_URL", "")
	if defaultGitlabURL == "" {
		defaultGitlabURL = helpers.GetEnv("CI_SERVER_URL", "")
	}
	gitlabURL = defaultGitlabURL

	defaultGitlabToken := helpers.GetEnv("ARGO_COMPARE_GITLAB_TOKEN", "")
	if defaultGitlabToken == "" {
		defaultGitlabToken = helpers.GetEnv("CI_JOB_TOKEN", "")
	}
	gitlabToken = defaultGitlabToken

	defaultProjectID := helpers.GetEnv("ARGO_COMPARE_GITLAB_PROJECT_ID", "")
	if defaultProjectID == "" {
		defaultProjectID = helpers.GetEnv("CI_PROJECT_ID", "")
	}
	gitlabProjectID = defaultProjectID

	gitlabMergeFromEnv := helpers.GetEnv("ARGO_COMPARE_GITLAB_MR_IID", "")
	if gitlabMergeFromEnv == "" {
		gitlabMergeFromEnv = helpers.GetEnv("CI_MERGE_REQUEST_IID", "")
	}
	if gitlabMergeFromEnv != "" {
		if parsed, err := strconv.Atoi(gitlabMergeFromEnv); err == nil {
			gitlabMergeIID = parsed
		}
	}

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

			configOptions := []app.ConfigOption{
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
			}

			switch provider := strings.ToLower(strings.TrimSpace(commentProvider)); provider {
			case "":
				// no-op
			case string(app.CommentProviderGitLab):
				configOptions = append(configOptions, app.WithCommentConfig(app.CommentConfig{
					Provider: app.CommentProviderGitLab,
					GitLab: app.GitLabCommentConfig{
						BaseURL:         gitlabURL,
						Token:           gitlabToken,
						ProjectID:       gitlabProjectID,
						MergeRequestIID: gitlabMergeIID,
					},
				}))
			default:
				return fmt.Errorf("unsupported comment provider %q", commentProvider)
			}

			cfg, err := app.NewConfig(
				targetBranch,
				configOptions...,
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
	cmd.Flags().StringVar(&commentProvider, "comment-provider", commentProvider, "Post diff comment using provider (gitlab)")
	cmd.Flags().StringVar(&gitlabURL, "gitlab-url", gitlabURL, "GitLab base URL (e.g., https://gitlab.com)")
	cmd.Flags().StringVar(&gitlabToken, "gitlab-token", gitlabToken, "GitLab personal access token")
	cmd.Flags().StringVar(&gitlabProjectID, "gitlab-project-id", gitlabProjectID, "GitLab project ID")
	cmd.Flags().IntVar(&gitlabMergeIID, "gitlab-merge-request-iid", gitlabMergeIID, "GitLab merge request IID")

	return cmd
}
