// Package anchor defines the .argo-compare.yml configuration file used by
// argo-compare to locate the ArgoCD Application affected by changes in a
// directory. The file acts as a forward pointer from "where changes happen"
// (e.g. Helm values, umbrella chart) to "the Application whose render is
// affected" — which may live in the same repo or a different one.
package anchor

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/afero"
	"gopkg.in/yaml.v3"
)

// Anchor is the top-level schema of a .argo-compare.yml file.
type Anchor struct {
	Application ApplicationRef `yaml:"application"`
}

// ApplicationRef points to an ArgoCD Application manifest somewhere in Git.
//
// Repo and Branch are optional; their resolution semantics (e.g. defaulting
// to the local repo, defaulting to a remote's default branch) are defined
// by the consumer that loads this struct.
//
// Path is required and identifies the Application YAML inside the target repo.
type ApplicationRef struct {
	Repo   string `yaml:"repo,omitempty"`
	Path   string `yaml:"path"`
	Branch string `yaml:"branch,omitempty"`
}

// ErrInvalidAnchor is returned for any structural or semantic failure
// while loading a .argo-compare.yml file. Callers should treat it as a
// hard error; the comparison cannot proceed without a valid anchor.
var ErrInvalidAnchor = errors.New("invalid .argo-compare.yml")

// Load reads and validates a .argo-compare.yml file from fs.
//
// The decoder is strict: unknown top-level keys, unknown keys inside the
// application block, and malformed YAML all yield an error. After
// decoding, application.path is required and must be non-empty.
func Load(fs afero.Fs, path string) (Anchor, error) {
	raw, err := afero.ReadFile(fs, path)
	if err != nil {
		return Anchor{}, fmt.Errorf("read anchor %q: %w", path, err)
	}

	var a Anchor
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&a); err != nil && !errors.Is(err, io.EOF) {
		// io.EOF means the document was empty or comment-only. Fall through to
		// the required-field check so the user sees a consistent message.
		return Anchor{}, fmt.Errorf("%w: parse %q: %w", ErrInvalidAnchor, path, err)
	}

	if strings.TrimSpace(a.Application.Path) == "" {
		return Anchor{}, fmt.Errorf("%w: %s: application.path is required", ErrInvalidAnchor, path)
	}

	return a, nil
}
