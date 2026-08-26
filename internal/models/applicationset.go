package models

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// KindApplicationSet is the manifest kind expanded into Applications before comparison.
const KindApplicationSet = "ApplicationSet"

// ErrNotApplicationSet signals that the provided manifest is not an ArgoCD ApplicationSet.
var ErrNotApplicationSet = errors.New("file is not an ApplicationSet")

// supportedGenerators lists the spec.generators kinds argo-compare can expand.
// Every other kind (matrix, clusters, scmProvider, ...) needs cluster or forge
// access this tool deliberately does not have.
var supportedGenerators = map[string]bool{
	"list": true,
	"git":  true,
}

// ApplicationSet models the subset of ArgoCD ApplicationSet fields needed to
// expand a manifest into the Applications it generates.
type ApplicationSet struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
	Spec ApplicationSetSpec `yaml:"spec"`
}

// ApplicationSetSpec holds the generator list and the Application template.
// Template stays a raw yaml.Node because it carries Go template actions that
// are only valid YAML once rendered.
type ApplicationSetSpec struct {
	GoTemplate        bool        `yaml:"goTemplate"`
	GoTemplateOptions []string    `yaml:"goTemplateOptions"`
	Generators        []Generator `yaml:"generators"`
	Template          yaml.Node   `yaml:"template"`
}

// Generator is one entry of spec.generators. Kinds records every key present so
// an unsupported generator can be named in the resulting error.
type Generator struct {
	Kinds []string
	List  *ListGenerator
	Git   *GitGenerator
}

// GitGenerator mirrors the git generator. Files and Values are decoded only to
// be rejected: both add parameters, so ignoring them would produce a diff that
// does not match ArgoCD.
type GitGenerator struct {
	RepoURL         string            `yaml:"repoURL"`
	Revision        string            `yaml:"revision"`
	Directories     []GitDirectory    `yaml:"directories"`
	PathParamPrefix string            `yaml:"pathParamPrefix"`
	Files           []GitFile         `yaml:"files"`
	Values          map[string]string `yaml:"values"`
	Template        yaml.Node         `yaml:"template"`
}

// GitDirectory is one entry of a git generator's directories list. Exclude
// entries take priority over the including ones, matching ArgoCD.
type GitDirectory struct {
	Path    string `yaml:"path"`
	Exclude bool   `yaml:"exclude"`
}

// GitFile is one entry of a git generator's files list.
type GitFile struct {
	Path string `yaml:"path"`
}

// ListGenerator mirrors the static list generator. ElementsYaml and Template
// are decoded only to be rejected: both change the generated output, so
// ignoring them would produce a diff that does not match ArgoCD.
type ListGenerator struct {
	Elements     []map[string]any `yaml:"elements"`
	ElementsYaml string           `yaml:"elementsYaml"`
	Template     yaml.Node        `yaml:"template"`
}

// UnmarshalYAML decodes a generator entry, recording the keys it declares.
func (g *Generator) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("generator entry must be a mapping")
	}

	for i := 0; i+1 < len(value.Content); i += 2 {
		key := value.Content[i].Value
		g.Kinds = append(g.Kinds, key)

		switch key {
		case "list":
			var list ListGenerator
			if err := value.Content[i+1].Decode(&list); err != nil {
				return fmt.Errorf("decode list generator: %w", err)
			}
			g.List = &list
		case "git":
			var gitGen GitGenerator
			if err := value.Content[i+1].Decode(&gitGen); err != nil {
				return fmt.Errorf("decode git generator: %w", err)
			}
			g.Git = &gitGen
		}
	}

	return nil
}

// Validate reports whether the ApplicationSet is one argo-compare can expand.
// Returns ErrEmptyFile for an empty manifest, ErrNotApplicationSet for another
// kind, or ErrUnsupportedAppConfiguration wrapped with the reason when the
// manifest uses legacy templating, omits spec.template, or an unsupported generator.
func (appSet *ApplicationSet) Validate() error {
	if appSet == nil {
		return ErrEmptyFile
	}

	if appSet.Kind == "" && appSet.Metadata.Name == "" && appSet.Metadata.Namespace == "" {
		return ErrEmptyFile
	}

	if appSet.Kind != KindApplicationSet {
		return ErrNotApplicationSet
	}

	// Legacy fasttemplate leaves unresolved tags in the output instead of
	// failing, which would surface downstream as an unrelated YAML or Helm error.
	if !appSet.Spec.GoTemplate {
		return fmt.Errorf("%w: only ApplicationSets with 'goTemplate: true' are supported", ErrUnsupportedAppConfiguration)
	}

	if appSet.Spec.Template.IsZero() {
		return fmt.Errorf("%w: spec.template is required", ErrUnsupportedAppConfiguration)
	}

	return appSet.validateGenerators()
}

// validateGenerators ensures every entry declares exactly one supported
// generator kind and uses no generator feature argo-compare cannot reproduce.
func (appSet *ApplicationSet) validateGenerators() error {
	if len(appSet.Spec.Generators) == 0 {
		return fmt.Errorf("%w: spec.generators must declare at least one generator", ErrUnsupportedAppConfiguration)
	}

	for _, generator := range appSet.Spec.Generators {
		if err := validateGenerator(generator); err != nil {
			return err
		}
	}

	return nil
}

// validateGenerator checks one spec.generators entry: exactly one kind, a
// supported one, and no field of it that argo-compare cannot reproduce.
func validateGenerator(generator Generator) error {
	if len(generator.Kinds) != 1 {
		return fmt.Errorf("%w: each entry of spec.generators must declare exactly one generator, got [%s]",
			ErrUnsupportedAppConfiguration, strings.Join(generator.Kinds, ", "))
	}

	if kind := generator.Kinds[0]; !supportedGenerators[kind] {
		return fmt.Errorf("%w: generator %q is not supported", ErrUnsupportedAppConfiguration, kind)
	}

	if generator.List != nil {
		return validateListGenerator(generator.List)
	}

	if generator.Git != nil {
		return validateGitGenerator(generator.Git)
	}

	return nil
}

// validateGitGenerator accepts a directory generator and rejects the fields
// that would change the generated Applications without being reproduced here.
func validateGitGenerator(gitGen *GitGenerator) error {
	switch {
	case gitGen.RepoURL == "":
		return fmt.Errorf("%w: git generator requires repoURL", ErrUnsupportedAppConfiguration)
	case len(gitGen.Files) > 0:
		return fmt.Errorf("%w: git generator field 'files' is not supported yet; only 'directories' is", ErrUnsupportedAppConfiguration)
	case len(gitGen.Directories) == 0:
		return fmt.Errorf("%w: git generator requires at least one 'directories' entry", ErrUnsupportedAppConfiguration)
	case len(gitGen.Values) > 0:
		return fmt.Errorf("%w: git generator field 'values' is not supported", ErrUnsupportedAppConfiguration)
	case gitGen.PathParamPrefix != "":
		return fmt.Errorf("%w: git generator field 'pathParamPrefix' is not supported; it would nest the path parameters", ErrUnsupportedAppConfiguration)
	case !gitGen.Template.IsZero():
		return fmt.Errorf("%w: generator-level template overrides are not supported", ErrUnsupportedAppConfiguration)
	}

	for _, dir := range gitGen.Directories {
		if dir.Path == "" {
			return fmt.Errorf("%w: every git generator 'directories' entry requires a path", ErrUnsupportedAppConfiguration)
		}
	}

	return nil
}

// validateListGenerator rejects the list generator fields that would change the
// generated Applications without being reproduced here.
func validateListGenerator(list *ListGenerator) error {
	if list.ElementsYaml != "" {
		return fmt.Errorf("%w: list generator field 'elementsYaml' is not supported", ErrUnsupportedAppConfiguration)
	}

	if !list.Template.IsZero() {
		return fmt.Errorf("%w: generator-level template overrides are not supported", ErrUnsupportedAppConfiguration)
	}

	return nil
}
