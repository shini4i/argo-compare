package app

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/op/go-logging"
)

type DiffStrategy interface {
	Present(result ComparisonResult) error
}

const currentFilePrintPattern = "▶ %s"

type StdoutStrategy struct {
	Log         *logging.Logger
	ShowAdded   bool
	ShowRemoved bool
}

type ExternalDiffStrategy struct {
	Log         *logging.Logger
	Tool        string
	ShowAdded   bool
	ShowRemoved bool
}

func (s StdoutStrategy) Present(result ComparisonResult) error {
	if result.IsEmpty() {
		s.Log.Info("No diff was found in rendered manifests!")
		return nil
	}

	if s.ShowAdded {
		s.printSection("added", result.Added)
	}

	if s.ShowRemoved {
		s.printSection("removed", result.Removed)
	}

	s.printSection("changed", result.Changed)

	return nil
}

func (s StdoutStrategy) printSection(operation string, entries []DiffOutput) {
	if len(entries) == 0 {
		return
	}

	fileText := "file"
	if len(entries) > 1 {
		fileText = "files"
	}

	s.Log.Infof("The following %d %s would be %s:", len(entries), fileText, operation)

	for _, entry := range entries {
		s.Log.Infof(currentFilePrintPattern, entry.File.Name)
		fmt.Println(entry.Diff)
	}
}

func (s ExternalDiffStrategy) Present(result ComparisonResult) error {
	if result.IsEmpty() {
		s.Log.Info("No diff was found in rendered manifests!")
		return nil
	}

	if s.ShowAdded {
		if err := s.runSection(result.Added); err != nil {
			return err
		}
	}

	if s.ShowRemoved {
		if err := s.runSection(result.Removed); err != nil {
			return err
		}
	}

	return s.runSection(result.Changed)
}

func (s ExternalDiffStrategy) runSection(entries []DiffOutput) error {
	for _, entry := range entries {
		if err := s.runTool(entry.Diff); err != nil {
			s.Log.Errorf("External diff tool failed for %s: %v", entry.File.Name, err)
		}
	}
	return nil
}

func (s ExternalDiffStrategy) runTool(diff string) error {
	cmd := exec.Command(s.Tool) // #nosec G204
	cmd.Stdin = strings.NewReader(diff)

	output, err := cmd.CombinedOutput()
	if len(output) > 0 {
		fmt.Println(string(output))
	}

	return err
}
