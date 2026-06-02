// Package command implements the CLI commands and flag parsing for argo-compare.
package command

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

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
	RunApp           func(ctx context.Context, cfg app.Config) error
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

// newBranchCommand constructs the "branch" subcommand which compares Applications against a target branch.
// The command requires a single argument (branch name), registers flags for single-file comparison, ignore
// patterns, Helm label preservation, output controls, comment provider, and GitLab connection details, and
// invokes the provided RunApp handler with a context that cancels on SIGINT/SIGTERM.
// The dropCache function is consulted to short-circuit execution when a cache purge was requested, and the
// debug function supplies the current debug mode for configuration.
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

			// Create a context that cancels on interrupt/terminate signals.
			ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			return opts.RunApp(ctx, cfg)
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
	cmd.Flags().BoolVar(&flags.validateManifests, "validate-manifests", flags.validateManifests, "Validate rendered manifests against Kubernetes schemas")
	cmd.Flags().StringVar(&flags.kubeconformPath, "kubeconform-path", flags.kubeconformPath, "Path to kubeconform binary")
	cmd.Flags().StringSliceVar(&flags.validateSkipKinds, "skip-validation-kinds", flags.validateSkipKinds, "Resource kinds to skip during validation (comma-separated)")
	cmd.Flags().StringSliceVar(&flags.validateSchemaLocations, "schema-location", flags.validateSchemaLocations, "Additional kubeconform -schema-location values (can be repeated or comma-separated)")
	cmd.Flags().StringVar(&flags.anchorFileName, "anchor-file", flags.anchorFileName, "Name of the file that marks an anchor directory (default .argo-compare.yml; empty disables discovery)")
	cmd.Flags().StringVar(&flags.gitUsername, "git-username", flags.gitUsername, "Username for HTTP Basic auth when cloning cross-repo anchored Applications (defaults to x-access-token; set to gitlab-ci-token for GitLab CI_JOB_TOKEN or your account name for Bitbucket)")
	cmd.Flags().StringVar(&flags.gitToken, "git-token", flags.gitToken, "Token (typically a PAT) for HTTP Basic auth when cloning cross-repo anchored Applications")

	return cmd
}

type branchFlags struct {
	file                    string
	ignore                  []string
	preserveHelmLabels      bool
	printAdded              bool
	printRemoved            bool
	fullOutput              bool
	commentProvider         string
	gitlabURL               string
	gitlabToken             string
	gitlabProjectID         string
	gitlabMergeIID          int
	validateManifests       bool
	kubeconformPath         string
	validateSkipKinds       []string
	validateSchemaLocations []string
	anchorFileName          string
	gitUsername             string
	gitToken                string
}

// loadBranchDefaults gathers branch flag defaults from the environment.
func loadBranchDefaults() branchFlags {
	defaults := branchFlags{}
	loadCommentDefaults(&defaults)
	loadValidationDefaults(&defaults)

	defaults.anchorFileName = helpers.GetEnv("ARGO_COMPARE_ANCHOR_FILE", app.DefaultAnchorFileName)
	defaults.gitUsername = helpers.GetEnv("ARGO_COMPARE_GIT_USERNAME", "")
	defaults.gitToken = helpers.GetEnv("ARGO_COMPARE_GIT_TOKEN", "")

	return defaults
}

// envWithFallback returns the primary env var's value, or the secondary's when
// the primary is unset/empty. Used to honor explicit ARGO_COMPARE_* overrides
// while falling back to GitLab CI's predefined variables.
func envWithFallback(primary, secondary string) string {
	if v := helpers.GetEnv(primary, ""); v != "" {
		return v
	}
	return helpers.GetEnv(secondary, "")
}

// splitCSV splits a comma-separated value into trimmed, non-empty items.
func splitCSV(s string) []string {
	var out []string
	for _, item := range strings.Split(s, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// loadCommentDefaults populates the GitLab comment-related defaults from the
// environment, preferring explicit ARGO_COMPARE_* vars over GitLab CI's.
func loadCommentDefaults(d *branchFlags) {
	provider := helpers.GetEnv("ARGO_COMPARE_COMMENT_PROVIDER", "")
	if provider == "" && helpers.GetEnv("GITLAB_CI", "") != "" && helpers.GetEnv("CI_MERGE_REQUEST_IID", "") != "" {
		provider = string(app.CommentProviderGitLab)
	}
	d.commentProvider = provider

	d.gitlabURL = envWithFallback("ARGO_COMPARE_GITLAB_URL", "CI_SERVER_URL")
	d.gitlabToken = envWithFallback("ARGO_COMPARE_GITLAB_TOKEN", "CI_JOB_TOKEN")
	d.gitlabProjectID = envWithFallback("ARGO_COMPARE_GITLAB_PROJECT_ID", "CI_PROJECT_ID")

	if mergeIID := envWithFallback("ARGO_COMPARE_GITLAB_MR_IID", "CI_MERGE_REQUEST_IID"); mergeIID != "" {
		if parsed, err := strconv.Atoi(mergeIID); err == nil {
			d.gitlabMergeIID = parsed
		}
	}
}

// loadValidationDefaults populates the manifest-validation defaults from the
// environment.
func loadValidationDefaults(d *branchFlags) {
	if validateStr := helpers.GetEnv("ARGO_COMPARE_VALIDATE_MANIFESTS", ""); validateStr != "" {
		if parsed, err := strconv.ParseBool(validateStr); err == nil {
			d.validateManifests = parsed
		} else {
			fmt.Fprintf(os.Stderr,
				"warning: ARGO_COMPARE_VALIDATE_MANIFESTS=%q is not a valid boolean; validation disabled (use 1/true/0/false)\n",
				validateStr)
		}
	}

	d.kubeconformPath = helpers.GetEnv("ARGO_COMPARE_KUBECONFORM_PATH", "")
	d.validateSkipKinds = splitCSV(helpers.GetEnv("ARGO_COMPARE_SKIP_VALIDATION_KINDS", ""))
	d.validateSchemaLocations = splitCSV(helpers.GetEnv("ARGO_COMPARE_KUBECONFORM_SCHEMA_LOCATIONS", ""))
}

// applyFullOutput toggles added/removed flags when full output is requested.
func (b *branchFlags) applyFullOutput() {
	if b.fullOutput {
		b.printAdded = true
		b.printRemoved = true
	}
}

// configOptions builds the list of config options based on flag values.
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
		app.WithValidateManifests(b.validateManifests),
		app.WithKubeconformPath(b.kubeconformPath),
		app.WithValidateSkipKinds(b.validateSkipKinds),
		app.WithValidateSchemaLocations(b.validateSchemaLocations),
		app.WithAnchorFileName(b.anchorFileName),
		app.WithGitAuth(b.gitUsername, b.gitToken),
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

// commentOption resolves the comment configuration, if any.
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
