package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/shini4i/argo-compare/internal/appset"
	"github.com/shini4i/argo-compare/internal/models"
	"github.com/shini4i/argo-compare/internal/ports"
	"github.com/shini4i/argo-compare/internal/ui"
	"github.com/spf13/afero"
)

// generatedPair holds one generated Application as it exists on each branch.
// A nil leg means the ApplicationSet stopped or started generating that name.
type generatedPair struct {
	name string
	src  *models.Application
	dst  *models.Application
}

// processChangedApplicationSet expands an ApplicationSet on both branches and
// compares each generated Application separately. Unlike a plain Application
// manifest, a change here can add or remove whole Applications, not only alter
// the manifests one renders.
func (a *App) processChangedApplicationSet(ctx context.Context, repo *GitRepo, file string, srcSet *models.ApplicationSet) (bool, error) {
	a.logger.Infof("===> Processing changed ApplicationSet: [%s]", ui.Cyan(file))

	originURL, err := repo.OriginURL()
	if err != nil {
		return false, err
	}
	// Skipping rather than failing keeps one unsupported manifest from breaking a
	// run, matching every other unsupported ApplicationSet configuration.
	if err := assertGitGeneratorsComparable(srcSet, originURL, a.cfg.TargetBranch); err != nil {
		a.logger.Warning(ui.Yellow(fmt.Sprintf("Skipping %s: %s", file, err)))
		return false, nil
	}

	headTree, err := repo.HeadTree()
	if err != nil {
		return false, err
	}

	srcApps, err := appset.Expand(srcSet, gitTree{tree: headTree})
	if err != nil {
		return false, fmt.Errorf("expand ApplicationSet %q: %w", file, err)
	}

	dstApps, err := a.targetApplicationSet(repo, file)
	if err != nil {
		return false, err
	}

	pairs := pairGeneratedApplications(srcApps, dstApps)
	a.logger.Infof("Generates %d Application(s) on this branch and %d on %s; comparing %d",
		len(srcApps), len(dstApps), a.cfg.TargetBranch, len(pairs))

	anyFailed := false
	for _, pair := range pairs {
		failed, err := a.compareGeneratedApplication(ctx, repo, file, pair)
		if err != nil {
			return anyFailed, err
		}
		if failed {
			anyFailed = true
		}
	}

	return anyFailed, nil
}

// targetApplicationSet expands the same ApplicationSet as it exists on the
// target branch. A manifest absent there yields no Applications, so every
// generated Application is reported as added.
func (a *App) targetApplicationSet(repo *GitRepo, file string) ([]models.Application, error) {
	content, err := repo.GetChangedFileRaw(a.cfg.TargetBranch, file)
	switch {
	case errors.Is(err, errGitFileDoesNotExist):
		a.logger.Warning(ui.Yellow(fmt.Sprintf(
			"The requested file %s does not exist in target branch %s, assuming it is a new ApplicationSet",
			file, a.cfg.TargetBranch)))
		return nil, nil
	case err != nil:
		return nil, err
	}

	appSet, err := a.parseTargetApplicationSet(repo, content)
	if err != nil {
		// Adding `goTemplate: true` to an existing manifest is the migration
		// this feature asks users to make, so an unexpandable baseline reports
		// every generated Application as new instead of failing the run.
		if unexpandable(err) {
			a.logger.Warning(ui.Yellow(fmt.Sprintf(
				"%s cannot be expanded on target branch %s (%s); treating every generated Application as new",
				file, a.cfg.TargetBranch, err)))
			return nil, nil
		}
		return nil, fmt.Errorf("parse ApplicationSet %q from branch %q: %w", file, a.cfg.TargetBranch, err)
	}

	tree, err := repo.MergeBaseTreeFor(a.cfg.TargetBranch)
	if err != nil {
		return nil, err
	}

	apps, err := appset.Expand(appSet, gitTree{tree: tree})
	if err != nil {
		return nil, fmt.Errorf("expand ApplicationSet %q from branch %q: %w", file, a.cfg.TargetBranch, err)
	}

	return apps, nil
}

// parseTargetApplicationSet validates the target-branch manifest, including
// that its git generators still name this repository. A generator pointing
// elsewhere would otherwise be matched against the local tree.
func (a *App) parseTargetApplicationSet(repo *GitRepo, content []byte) (*models.ApplicationSet, error) {
	appSet, err := parseApplicationSetContent(content)
	if err != nil {
		return nil, err
	}

	originURL, err := repo.OriginURL()
	if err != nil {
		return nil, err
	}

	if err := assertGitGeneratorsComparable(appSet, originURL, a.cfg.TargetBranch); err != nil {
		return nil, err
	}

	return appSet, nil
}

// unexpandable reports whether err means the manifest is one argo-compare
// chooses not to expand, as opposed to one it could not read or decode.
func unexpandable(err error) bool {
	return errors.Is(err, models.ErrUnsupportedAppConfiguration) ||
		errors.Is(err, models.ErrNotApplicationSet) ||
		errors.Is(err, models.ErrEmptyFile)
}

// pairGeneratedApplications matches generated Applications by name, keeping
// source order and appending the names only the target branch still generates.
func pairGeneratedApplications(srcApps, dstApps []models.Application) []generatedPair {
	dstByName := make(map[string]*models.Application, len(dstApps))
	for i := range dstApps {
		dstByName[dstApps[i].Metadata.Name] = &dstApps[i]
	}

	pairs := make([]generatedPair, 0, len(srcApps)+len(dstApps))
	matched := make(map[string]bool, len(srcApps))

	for i := range srcApps {
		name := srcApps[i].Metadata.Name
		matched[name] = true
		pairs = append(pairs, generatedPair{name: name, src: &srcApps[i], dst: dstByName[name]})
	}

	for i := range dstApps {
		if name := dstApps[i].Metadata.Name; !matched[name] {
			pairs = append(pairs, generatedPair{name: name, dst: &dstApps[i]})
		}
	}

	return pairs
}

// compareGeneratedApplication renders and diffs one generated Application.
// Applications present on only one branch are skipped unless the matching
// --print-added-manifests / --print-removed-manifests flag is set, mirroring
// how a newly added Application manifest is handled.
func (a *App) compareGeneratedApplication(ctx context.Context, repo *GitRepo, file string, pair generatedPair) (validationFailed bool, err error) {
	if a.skipOneSidedPair(pair) {
		return false, nil
	}

	a.logger.Infof("===> Comparing generated Application: [%s]", ui.Cyan(pair.name))

	tmpDir, err := afero.TempDir(a.fs, a.cfg.TempDirBase, "argo-compare-appset-")
	if err != nil {
		return false, err
	}

	defer func() {
		if removeErr := (afero.Afero{Fs: a.fs}).RemoveAll(tmpDir); err == nil && removeErr != nil {
			err = removeErr
		}
	}()

	validationResults := make(map[string]ports.ValidationResult)

	proceed, err := a.renderGeneratedLegs(ctx, repo, pair, tmpDir, validationResults)
	if err != nil {
		return false, err
	}
	if !proceed {
		return false, nil
	}

	if err = a.runComparison(ctx, tmpDir, generatedLabel(file, pair.name), validationResults); err != nil {
		return false, err
	}

	return anyValidationFailed(validationResults), nil
}

// generatedLabel names a generated Application in diff output and merge request
// comments. Without the name every Application from one ApplicationSet would be
// labelled with the same manifest path.
func generatedLabel(file, name string) string {
	return fmt.Sprintf("%s [%s]", file, name)
}

// skipOneSidedPair reports whether an Application that exists on only one
// branch is suppressed by the print flags, logging the reason when it is.
func (a *App) skipOneSidedPair(pair generatedPair) bool {
	switch {
	case pair.dst == nil && !a.cfg.PrintAddedManifests:
		a.logger.Infof("Skipping added Application [%s]; enable --print-added-manifests to render it", ui.Cyan(pair.name))
		return true
	case pair.src == nil && !a.cfg.PrintRemovedManifests:
		a.logger.Infof("Skipping removed Application [%s]; enable --print-removed-manifests to render it", ui.Cyan(pair.name))
		return true
	default:
		return false
	}
}

// renderGeneratedLegs renders whichever branches still generate the Application.
// A leg with no Application renders nothing, so the comparison reports every
// manifest on the other side as added or removed. It returns false when the
// comparison should be skipped for want of a baseline to diff against.
func (a *App) renderGeneratedLegs(ctx context.Context, repo *GitRepo, pair generatedPair, tmpDir string, validationResults map[string]ports.ValidationResult) (bool, error) {
	if pair.src != nil {
		if err := a.renderGeneratedLeg(ctx, repo, *pair.src, TargetTypeSource, tmpDir, validationResults); err != nil {
			return false, err
		}
	}

	if pair.dst == nil {
		return true, nil
	}

	// A path-based chart the branch adds for the first time is absent from the
	// merge-base tree even though both branches generate the Application, so the
	// baseline leg has nothing to render. That is a new chart, not a failure.
	destErr := a.renderGeneratedLeg(ctx, repo, *pair.dst, TargetTypeDestination, tmpDir, validationResults)
	switch {
	case destErr == nil:
		return true, nil
	case errors.Is(destErr, ErrChartPathNotInTree):
		a.logger.Warning(ui.Yellow(fmt.Sprintf(
			"The chart for generated Application [%s] does not exist in target branch %s, assuming it is new",
			pair.name, a.cfg.TargetBranch)))
		return a.cfg.PrintAddedManifests, nil
	default:
		return false, destErr
	}
}

// renderGeneratedLeg renders one leg of a generated Application. No File is set
// on the Target: the Application came from expansion, not from a manifest on disk.
func (a *App) renderGeneratedLeg(ctx context.Context, repo *GitRepo, app models.Application, leg, tmpDir string, validationResults map[string]ports.ValidationResult) error {
	target := Target{
		CmdRunner:           a.cmdRunner,
		FileReader:          a.fileReader,
		HelmProcessor:       a.helmProcessor,
		Globber:             a.globber,
		CacheDir:            a.cfg.CacheDir,
		TmpDir:              tmpDir,
		CredentialProviders: a.activeProviders,
		Log:                 a.logger,
		Type:                leg,
		App:                 app,
	}

	return a.renderTarget(ctx, repo, &target, leg, validationResults)
}
