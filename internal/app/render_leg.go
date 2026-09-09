package app

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/shini4i/argo-compare/internal/ports"
)

// renderLeg runs the Helm pipeline for one comparison leg of every entry flow: materialize
// the chart, resolve "$ref" values files, write inline values, render into target.TmpDir and,
// on the source leg, validate into validationResults. An empty repoRoot is resolved from the
// working directory only when a path-based or same-repo ref source needs it.
func (a *App) renderLeg(ctx context.Context, repo *GitRepo, target *Target, repoRoot string, validationResults map[string]ports.ValidationResult) error {
	if err := target.ClassifySources(); err != nil {
		return err
	}

	if err := a.materializeChart(ctx, repo, target, repoRoot); err != nil {
		return err
	}

	if err := a.materializeRefSourcesForLeg(ctx, repo, target, repoRoot); err != nil {
		return err
	}

	if err := target.generateValuesFiles(); err != nil {
		return err
	}

	if err := target.renderAppSources(ctx); err != nil {
		return err
	}

	a.runManifestValidation(ctx, target.Type, target.TmpDir, validationResults)
	return nil
}

// materializeChart puts one leg's chart into the layout the renderer expects: a registry chart
// is pulled and extracted; a path-based chart is copied from the working tree (source leg) or
// the merge-base tree of the target branch (destination leg) and then has its subchart
// dependencies built.
func (a *App) materializeChart(ctx context.Context, repo *GitRepo, target *Target, repoRoot string) error {
	if !target.PathBased() {
		if err := target.ensureHelmCharts(ctx); err != nil {
			return err
		}
		return target.extractCharts(ctx)
	}

	switch target.Type {
	case TargetTypeSource:
		root, err := ensureRepoRoot(repoRoot)
		if err != nil {
			return fmt.Errorf("resolve repo root for path-based source: %w", err)
		}
		if err := target.MaterializeChartFromWorkingTree(ctx, a.fs, root); err != nil {
			return err
		}
	case TargetTypeDestination:
		tree, err := repo.MergeBaseTreeFor(a.cfg.TargetBranch)
		if err != nil {
			return err
		}
		if err := target.MaterializeChartFromTree(ctx, a.fs, tree); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown render leg %q", target.Type)
	}

	return target.BuildChartDependencies(ctx)
}

// ensureRepoRoot returns repoRoot as given, or the root of the repository containing the
// working directory when repoRoot is empty.
func ensureRepoRoot(repoRoot string) (string, error) {
	if repoRoot != "" {
		return repoRoot, nil
	}
	return GetGitRepoRoot()
}

// runManifestValidation validates rendered source manifests and records the outcome, including
// a validator invocation failure. Destination manifests are skipped: they reflect the target
// branch as it already is, so their breakage is not this change's to gate.
func (a *App) runManifestValidation(ctx context.Context, fileType, tmpDir string, validationResults map[string]ports.ValidationResult) {
	if a.validator == nil || fileType != TargetTypeSource {
		return
	}
	// Rendered manifests land at <tmpDir>/templates/<src|dst> (set by RenderAppSource).
	manifests := filepath.Join(tmpDir, "templates", fileType)
	result, err := a.validator.Validate(ctx, fileType, manifests)
	if err != nil {
		a.logger.Warningf("Manifest validation failed: %v", err)
		validationResults[fileType] = ports.ValidationResult{
			Target:          fileType,
			InvocationError: err.Error(),
		}
		return
	}
	validationResults[fileType] = result
	if !result.Valid {
		a.logger.Warningf("Validation errors found: %d issues", result.ErrorCount)
	}
}
