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

		if key == "list" {
			var list ListGenerator
			if err := value.Content[i+1].Decode(&list); err != nil {
				return fmt.Errorf("decode list generator: %w", err)
			}
			g.List = &list
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
		if len(generator.Kinds) != 1 {
			return fmt.Errorf("%w: each entry of spec.generators must declare exactly one generator, got [%s]",
				ErrUnsupportedAppConfiguration, strings.Join(generator.Kinds, ", "))
		}

		if kind := generator.Kinds[0]; !supportedGenerators[kind] {
			return fmt.Errorf("%w: generator %q is not supported", ErrUnsupportedAppConfiguration, kind)
		}

		if generator.List != nil {
			if err := validateListGenerator(generator.List); err != nil {
				return err
			}
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
