package app

import (
	"fmt"
	"path/filepath"

	"github.com/shini4i/argo-compare/internal/models"
	"github.com/shini4i/argo-compare/internal/ports"
	"gopkg.in/yaml.v3"
)

// argocdSourceOverride is the subset of an .argocd-source[-<app>].yaml file we
// consume. ArgoCD lets these files override any spec.source field; we only read
// helm.parameters, which is what argo-watcher / Argo CD Image Updater write when
// recording image bumps. Other override keys (values, valueFiles, kustomize)
// are intentionally ignored — see docs for the supported subset.
type argocdSourceOverride struct {
	Helm struct {
		Parameters []models.HelmParameter `yaml:"parameters"`
	} `yaml:"helm"`
}

// resolveHelmParameters merges helm parameters in ArgoCD's documented order:
// the Application's inline spec.source.helm.parameters first, then the generic
// .argocd-source.yaml, then the app-specific .argocd-source-<appName>.yaml
// committed in the chart's source directory. Later definitions override earlier
// ones by name; the result is deduplicated, preserving first-seen order so the
// rendered `--set` flags are deterministic.
//
// Override files are read from chartDir, the materialized chart directory. For
// path-based sources ArgoCD looks for them at the root of spec.source.path,
// which materialization copies verbatim, so they land beside the chart. Absent
// files are a no-op (FileReader returns (nil, nil) for a missing path), so this
// is safe to call for registry-based sources too — they simply have no override
// file and contribute only their inline parameters.
func resolveHelmParameters(reader ports.FileReader, source *models.Source, chartDir, appName string) ([]models.HelmParameter, error) {
	merged := newParamMerge()
	if source != nil {
		merged.apply(source.Helm.Parameters)
	}

	for _, name := range overrideFileNames(appName) {
		path, ok := safeOverridePath(chartDir, name)
		if !ok {
			// appName embedded path traversal in the filename; skip rather than
			// read a file outside chartDir.
			continue
		}
		params, err := readOverrideParameters(reader, path)
		if err != nil {
			return nil, fmt.Errorf("read source override %q: %w", name, err)
		}
		merged.apply(params)
	}

	return merged.ordered(), nil
}

// overrideFileNames lists the ArgoCD source-override files to consult, generic
// first so the app-specific file can override it by parameter name. An empty
// appName yields only the generic file.
func overrideFileNames(appName string) []string {
	names := []string{".argocd-source.yaml"}
	if appName != "" {
		names = append(names, fmt.Sprintf(".argocd-source-%s.yaml", appName))
	}
	return names
}

// safeOverridePath joins fileName onto chartDir and confirms the result still
// sits directly inside chartDir. The Application's metadata.name is untrusted
// (PR-author-controlled) and flows into the app-specific filename; without this
// guard a name like "../../etc/x" could redirect the read to an arbitrary file
// whose contents would surface in the rendered diff.
func safeOverridePath(chartDir, fileName string) (string, bool) {
	path := filepath.Join(chartDir, fileName)
	if filepath.Dir(path) != filepath.Clean(chartDir) {
		return "", false
	}
	return path, true
}

// readOverrideParameters reads and parses a single override file's
// helm.parameters. A missing or empty file yields (nil, nil).
func readOverrideParameters(reader ports.FileReader, path string) ([]models.HelmParameter, error) {
	data, err := reader.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	var override argocdSourceOverride
	if err := yaml.Unmarshal(data, &override); err != nil {
		return nil, err
	}
	return override.Helm.Parameters, nil
}

// paramMerge accumulates helm parameters keyed by name while preserving the
// order in which each name was first seen, so a later source (override file)
// can replace an earlier one's value without reordering the final flag list.
type paramMerge struct {
	order  []string
	byName map[string]models.HelmParameter
}

func newParamMerge() *paramMerge {
	return &paramMerge{byName: make(map[string]models.HelmParameter)}
}

func (m *paramMerge) apply(params []models.HelmParameter) {
	for _, p := range params {
		if _, seen := m.byName[p.Name]; !seen {
			m.order = append(m.order, p.Name)
		}
		m.byName[p.Name] = p
	}
}

func (m *paramMerge) ordered() []models.HelmParameter {
	if len(m.order) == 0 {
		return nil
	}
	out := make([]models.HelmParameter, 0, len(m.order))
	for _, name := range m.order {
		out = append(out, m.byName[name])
	}
	return out
}
