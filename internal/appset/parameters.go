package appset

import (
	"fmt"
	"sort"

	"github.com/shini4i/argo-compare/internal/models"
)

// parameterCollector flattens generators into the parameter sets that each
// produce one Application. It lists the tree at most once per shape, since
// every git generator in a manifest reads the same branch.
type parameterCollector struct {
	tree    Tree
	options []string

	params []map[string]any

	dirs        []string
	files       []string
	listedDirs  bool
	listedFiles bool
}

// collect appends the parameter sets one generator produces.
func (c *parameterCollector) collect(generator models.Generator) error {
	if generator.List != nil {
		c.params = append(c.params, generator.List.Elements...)
		return nil
	}

	if generator.Git == nil {
		return nil
	}

	if c.tree == nil {
		return fmt.Errorf("%w: git generators need a repository to read", models.ErrUnsupportedAppConfiguration)
	}

	if len(generator.Git.Directories) > 0 {
		return c.collectDirectories(generator.Git)
	}

	return c.collectFiles(generator.Git)
}

// collectDirectories generates one parameter set per matching directory.
func (c *parameterCollector) collectDirectories(gitGen *models.GitGenerator) error {
	if !c.listedDirs {
		dirs, err := c.tree.Directories()
		if err != nil {
			return fmt.Errorf("list directories for git generator: %w", err)
		}
		c.dirs, c.listedDirs = dirs, true
	}

	for _, dir := range matchDirectories(c.dirs, gitGen.Directories) {
		params := directoryParams(dir)
		if err := c.applyValues(params, gitGen.Values); err != nil {
			return err
		}
		c.params = append(c.params, params)
	}

	return nil
}

// collectFiles generates parameter sets from the contents of matching files.
// One file yields several sets when it holds a sequence.
func (c *parameterCollector) collectFiles(gitGen *models.GitGenerator) error {
	if !c.listedFiles {
		files, err := c.tree.Files()
		if err != nil {
			return fmt.Errorf("list files for git generator: %w", err)
		}
		c.files, c.listedFiles = files, true
	}

	for _, file := range matchFiles(c.files, gitGen.Files) {
		content, err := c.tree.ReadFile(file)
		if err != nil {
			return fmt.Errorf("read %s for git generator: %w", file, err)
		}

		sets, err := fileParams(file, content)
		if err != nil {
			return err
		}

		for _, params := range sets {
			if err := c.applyValues(params, gitGen.Values); err != nil {
				return err
			}
			c.params = append(c.params, params)
		}
	}

	return nil
}

// applyValues renders the generator's values block against the parameters built
// so far and exposes the results as .values. Results are collected separately
// and merged at the end, so one entry cannot reference another.
func (c *parameterCollector) applyValues(params map[string]any, values map[string]string) error {
	if len(values) == 0 {
		return nil
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	rendered := make(map[string]string, len(values))
	for _, key := range keys {
		value, err := render(values[key], params, c.options)
		if err != nil {
			return fmt.Errorf("render values.%s: %w", key, err)
		}
		rendered[key] = value
	}

	params["values"] = rendered

	return nil
}
