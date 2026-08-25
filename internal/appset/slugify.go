package appset

import (
	"fmt"
	"sync"

	"github.com/gosimple/slug"
)

// Defaults ArgoCD's slugify applies when a template omits the optional arguments.
const (
	defaultSlugMaxLength     = 50
	defaultSlugSmartTruncate = true
)

// slugify turns a value into a URL-safe slug, following ArgoCD's argument
// order: the name is always last, an optional maximum length first, and an
// optional smart-truncate flag second. A wrongly typed argument is an error,
// because a slug silently falling back to "" names an Application nothing has.
func slugify(args ...any) (string, error) {
	maxLength := defaultSlugMaxLength
	smartTruncate := defaultSlugSmartTruncate
	name := ""

	for i, arg := range args {
		ok := true

		switch {
		case i == len(args)-1:
			name, ok = arg.(string)
		case i == 0:
			maxLength, ok = arg.(int)
		case i == 1:
			smartTruncate, ok = arg.(bool)
		}

		if !ok {
			return "", fmt.Errorf("slugify: argument %d is %T; expected an optional length first, an optional flag second, and the name last", i, arg)
		}
	}

	// slug truncates by slicing, so a negative length panics inside the library.
	if maxLength < 0 {
		return "", fmt.Errorf("slugify: length %d must not be negative", maxLength)
	}

	return makeSlug(normalize(name), maxLength, smartTruncate), nil
}

// slugSettings serialises the whole save, configure, render and restore around
// gosimple/slug's package-level settings, which it offers no per-call
// alternative to. Without it two concurrent renders read each other's
// truncation options and produce wrongly named Applications.
var slugSettings sync.Mutex

// makeSlug applies the package-level configuration slug exposes and restores it
// afterwards, so one call cannot change the shape of the next.
func makeSlug(name string, maxLength int, smartTruncate bool) string {
	slugSettings.Lock()
	defer slugSettings.Unlock()

	previousMaxLength, previousSmartTruncate := slug.MaxLength, slug.EnableSmartTruncate
	defer func() {
		slug.MaxLength, slug.EnableSmartTruncate = previousMaxLength, previousSmartTruncate
	}()

	slug.MaxLength = maxLength
	slug.EnableSmartTruncate = smartTruncate

	return slug.Make(name)
}
