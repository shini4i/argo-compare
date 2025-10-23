package app

import (
	"errors"
	"fmt"
	"strings"

	"github.com/op/go-logging"
	"github.com/shini4i/argo-compare/internal/comment"
)

// CommentStrategy delivers comparison results to an upstream comment system.
type CommentStrategy struct {
	Log         *logging.Logger
	Poster      comment.Poster
	ShowAdded   bool
	ShowRemoved bool
}

// Present formats comparison results and pushes them as a single comment.
func (s CommentStrategy) Present(result ComparisonResult) error {
	if s.Poster == nil {
		return errors.New("comment strategy requires a poster implementation")
	}
	if s.Log == nil {
		return errors.New("comment strategy requires a logger")
	}

	body := buildCommentBody(result, s.ShowAdded, s.ShowRemoved)
	if err := s.Poster.Post(body); err != nil {
		return fmt.Errorf("post diff comment: %w", err)
	}

	if result.IsEmpty() {
		s.Log.Info("Posted comment summarizing absence of manifest changes")
	} else {
		s.Log.Info("Posted comment with manifest diff summary")
	}

	return nil
}

func buildCommentBody(result ComparisonResult, showAdded, showRemoved bool) string {
	var b strings.Builder

	b.WriteString("## Argo Compare Results\n\n")

	if result.IsEmpty() {
		b.WriteString("No manifest differences detected :white_check_mark:\n")
		return b.String()
	}

	if showAdded {
		writeSection(&b, "Added", result.Added)
	}
	if showRemoved {
		writeSection(&b, "Removed", result.Removed)
	}
	writeSection(&b, "Changed", result.Changed)

	return strings.TrimSpace(b.String()) + "\n"
}

func writeSection(builder *strings.Builder, title string, entries []DiffOutput) {
	if len(entries) == 0 {
		return
	}

	fmt.Fprintf(builder, "### %s (%d)\n\n", title, len(entries))

	for _, entry := range entries {
		fileName := strings.TrimPrefix(entry.File.Name, "/")
		diff := strings.TrimRight(entry.Diff, "\n")

		builder.WriteString("<details>\n")
		fmt.Fprintf(builder, "<summary>%s</summary>\n\n", fileName)
		builder.WriteString("```diff\n")
		builder.WriteString(diff)
		if !strings.HasSuffix(diff, "\n") {
			builder.WriteString("\n")
		}
		builder.WriteString("```\n")
		builder.WriteString("</details>\n\n")
	}
}
