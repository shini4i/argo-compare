// Package appset expands ArgoCD ApplicationSet manifests into the Applications
// they generate, so the existing render-and-diff pipeline can process each one.
package appset

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"text/template"

	"github.com/Masterminds/sprig/v3"
	"github.com/shini4i/argo-compare/internal/models"
)

// maxDNSNameLength is the length normalize truncates to, per RFC 1123 subdomain.
const maxDNSNameLength = 253

// maxRenderedBytes caps one rendered field. Real Application fields are far
// smaller; the cap stops a runaway template from growing the value that every
// later stage — the diff, a merge request comment — carries.
const maxRenderedBytes = 1 << 20

// errRenderTooLarge reports a rendered field over maxRenderedBytes.
var errRenderTooLarge = errors.New("rendered field exceeds the 1 MiB limit")

// limitedBuffer collects rendered output and fails once it passes limit.
type limitedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (w *limitedBuffer) Write(p []byte) (int, error) {
	if w.buf.Len()+len(p) > w.limit {
		return 0, errRenderTooLarge
	}
	return w.buf.Write(p)
}

// invalidDNSNameChars matches every character normalize replaces with a hyphen.
var invalidDNSNameChars = regexp.MustCompile("[^-a-z0-9.]")

// supportedTemplateOptions are the goTemplateOptions values text/template
// accepts. Option panics on anything else, so entries are checked up front.
var supportedTemplateOptions = map[string]bool{
	"missingkey=default": true,
	"missingkey=invalid": true,
	"missingkey=zero":    true,
	"missingkey=error":   true,
}

// templateFuncs is the function map ApplicationSet templates render with.
var templateFuncs = buildTemplateFuncs()

// buildTemplateFuncs mirrors ArgoCD's ApplicationSet function map: Sprig
// without the functions that read the environment, plus normalize.
func buildTemplateFuncs() template.FuncMap {
	funcs := sprig.TxtFuncMap()

	// A manifest author can open a pull request; rendering runs in CI next to
	// REPO_CREDS_* and posts its diff as a comment. env/expandenv would turn
	// that into credential disclosure, and getHostByName into a DNS probe.
	delete(funcs, "env")
	delete(funcs, "expandenv")
	delete(funcs, "getHostByName")

	funcs["normalize"] = normalize
	funcs["slugify"] = slugify
	funcs["toYaml"] = toYAML
	funcs["fromYaml"] = fromYAML
	funcs["fromYamlArray"] = fromYAMLArray

	return funcs
}

// normalize sanitizes a string into a valid DNS subdomain name: lowercase, no
// more than 253 characters, only alphanumerics, '-' and '.', and starting and
// ending with an alphanumeric character.
func normalize(name string) string {
	name = strings.ToLower(name)
	name = invalidDNSNameChars.ReplaceAllString(name, "-")

	if len(name) > maxDNSNameLength {
		name = name[:maxDNSNameLength]
	}

	return strings.Trim(name, "-.")
}

// render executes the ApplicationSet template against a single parameter set.
func render(text string, params map[string]any, options []string) (string, error) {
	if err := validateTemplateOptions(options); err != nil {
		return "", err
	}

	parsed, err := template.New("applicationset").Funcs(templateFuncs).Option(options...).Parse(text)
	if err != nil {
		return "", fmt.Errorf("parse spec.template: %w", err)
	}

	buf := limitedBuffer{limit: maxRenderedBytes}
	if err := parsed.Execute(&buf, params); err != nil {
		if errors.Is(err, errRenderTooLarge) {
			return "", errRenderTooLarge
		}
		return "", fmt.Errorf("render spec.template: %w", err)
	}

	return buf.buf.String(), nil
}

// validateTemplateOptions rejects goTemplateOptions entries text/template does
// not recognise, which it would otherwise answer with a panic.
func validateTemplateOptions(options []string) error {
	for _, option := range options {
		if !supportedTemplateOptions[option] {
			return fmt.Errorf("%w: goTemplateOptions entry %q is not recognised", models.ErrUnsupportedAppConfiguration, option)
		}
	}

	return nil
}
