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
	Log             *logging.Logger
	Poster          comment.Poster
	ShowAdded       bool
	ShowRemoved     bool
	ApplicationPath string
}

const (
	gitlabNoteLengthLimit = 1_000_000
	commentPartReserve    = 32
)

// Present formats comparison results and pushes them as one or more comments depending on size.
func (s CommentStrategy) Present(result ComparisonResult) error {
	if s.Poster == nil {
		return errors.New("comment strategy requires a poster implementation")
	}
	if s.Log == nil {
		return errors.New("comment strategy requires a logger")
	}

	bodies := buildCommentBodies(result, s.ShowAdded, s.ShowRemoved, s.ApplicationPath)
	for idx, body := range bodies {
		bodyToPost := body
		if len(bodies) > 1 {
			bodyToPost = ensureTrailingNewline(strings.TrimRight(body, "\n") + fmt.Sprintf("\n\n_Part %d of %d_", idx+1, len(bodies)))
		}
		if err := s.Poster.Post(bodyToPost); err != nil {
			if len(bodies) > 1 {
				return fmt.Errorf("post diff comment (part %d/%d): %w", idx+1, len(bodies), err)
			}
			return fmt.Errorf("post diff comment: %w", err)
		}
	}

	app := strings.TrimSpace(s.ApplicationPath)
	if app == "" {
		app = "unknown application"
	}

	if result.IsEmpty() {
		s.Log.Infof("Posted comment summarizing absence of manifest changes for %s", app)
	} else {
		if len(bodies) > 1 {
			s.Log.Infof("Posted %d comments with manifest diff summary for %s", len(bodies), app)
		} else {
			s.Log.Infof("Posted comment with manifest diff summary for %s", app)
		}
	}

	return nil
}

func buildCommentBodies(result ComparisonResult, showAdded, showRemoved bool, applicationPath string) []string {
	appLabel := strings.TrimSpace(applicationPath)
	if appLabel == "" {
		appLabel = "unknown"
	}
	appDisplay := strings.ReplaceAll(appLabel, "`", "\\`")

	var headerBuilder strings.Builder
	headerBuilder.WriteString("## Argo Compare Results\n\n")
	headerBuilder.WriteString(fmt.Sprintf("**Application:** `%s`\n\n", appDisplay))

	if summary := buildSummaryLines(result, showAdded, showRemoved); summary != "" {
		headerBuilder.WriteString(summary)
	}

	header := headerBuilder.String()
	if result.IsEmpty() {
		return []string{ensureTrailingNewline(header + "No manifest differences detected :white_check_mark:\n")}
	}

	maxPerComment := gitlabNoteLengthLimit - commentPartReserve
	if maxPerComment <= 0 {
		maxPerComment = gitlabNoteLengthLimit
	}
	if len(header) >= maxPerComment {
		maxPerComment = len(header) + 1
	}

	maxChunkLen := maxPerComment - len(header)
	if maxChunkLen <= 0 {
		maxChunkLen = maxPerComment / 2
	}

	chunks := collectDiffChunks(result, showAdded, showRemoved, maxChunkLen)
	return assembleCommentBodies(header, chunks)
}

func buildSummaryLines(result ComparisonResult, showAdded, showRemoved bool) string {
	var lines []string

	if showAdded || len(result.Added) > 0 {
		label := fmt.Sprintf("- Added: %d", len(result.Added))
		if !showAdded && len(result.Added) > 0 {
			label += " (not shown)"
		}
		lines = append(lines, label)
	}

	if showRemoved || len(result.Removed) > 0 {
		label := fmt.Sprintf("- Removed: %d", len(result.Removed))
		if !showRemoved && len(result.Removed) > 0 {
			label += " (not shown)"
		}
		lines = append(lines, label)
	}

	lines = append(lines, fmt.Sprintf("- Changed: %d", len(result.Changed)))

	if len(lines) == 0 {
		return ""
	}

	return "**Summary**\n" + strings.Join(lines, "\n") + "\n\n"
}

func collectDiffChunks(result ComparisonResult, showAdded, showRemoved bool, maxChunkLen int) []string {
	var chunks []string

	if showAdded {
		chunks = append(chunks, buildDiffChunks("Added", result.Added, maxChunkLen)...)
	} else if len(result.Added) > 0 {
		chunks = append(chunks, buildOmittedNotice("Added", len(result.Added)))
	}

	if showRemoved {
		chunks = append(chunks, buildDiffChunks("Removed", result.Removed, maxChunkLen)...)
	} else if len(result.Removed) > 0 {
		chunks = append(chunks, buildOmittedNotice("Removed", len(result.Removed)))
	}

	chunks = append(chunks, buildDiffChunks("Changed", result.Changed, maxChunkLen)...)

	return chunks
}

func buildOmittedNotice(section string, count int) string {
	return fmt.Sprintf("> %s manifests (%d) are present but not shown with the current settings.\n\n", section, count)
}

func buildDiffChunks(section string, entries []DiffOutput, maxChunkLen int) []string {
	var chunks []string
	for _, entry := range entries {
		chunks = append(chunks, buildDiffEntryChunks(section, entry, maxChunkLen)...)
	}
	return chunks
}

func buildDiffEntryChunks(section string, entry DiffOutput, maxChunkLen int) []string {
	fileName := strings.TrimPrefix(entry.File.Name, "/")
	if fileName == "" {
		fileName = "unknown"
	}

	diff := strings.TrimRight(entry.Diff, "\n")
	if diff == "" {
		diff = "(no diff output)"
	}

	if isCRDManifest(entry) {
		return []string{fmt.Sprintf("> CRD manifest `%s` detected in the %s section. Diff omitted to keep merge request comments concise. Review the job logs for full details.\n\n", fileName, strings.ToLower(section))}
	}

	diff = sanitizeDiffHeaders(section, fileName, diff)

	closing := "\n```\n</details>\n\n"
	var chunks []string
	part := 1
	remaining := diff

	for len(remaining) > 0 {
		summaryLabel := fmt.Sprintf("%s • %s", section, fileName)
		if part > 1 {
			summaryLabel = fmt.Sprintf("%s • %s (part %d)", section, fileName, part)
		}

		opening := fmt.Sprintf("<details>\n<summary>%s</summary>\n\n```diff\n", summaryLabel)
		available := maxChunkLen - len(opening) - len(closing)
		if available < 1 {
			available = 1
		}

		chunkDiff, rest := splitDiffContent(remaining, available)

		var builder strings.Builder
		builder.WriteString(opening)
		builder.WriteString(chunkDiff)
		if !strings.HasSuffix(chunkDiff, "\n") {
			builder.WriteString("\n")
		}
		builder.WriteString(closing)

		chunks = append(chunks, builder.String())
		remaining = rest
		part++
	}

	return chunks
}

func splitDiffContent(content string, limit int) (string, string) {
	if limit <= 0 || len(content) <= limit {
		return content, ""
	}

	cut := strings.LastIndex(content[:limit], "\n")
	if cut <= 0 {
		cut = limit
	}

	chunk := content[:cut]
	remaining := content[cut:]
	return chunk, strings.TrimPrefix(remaining, "\n")
}

func assembleCommentBodies(header string, chunks []string) []string {
	maxPerComment := gitlabNoteLengthLimit - commentPartReserve
	if maxPerComment <= 0 {
		maxPerComment = gitlabNoteLengthLimit
	}
	if len(header) >= maxPerComment {
		maxPerComment = len(header) + 1
	}

	var bodies []string
	var builder strings.Builder
	builder.WriteString(header)

	for _, chunk := range includeNonEmptyChunks(chunks) {
		if builder.Len()+len(chunk) > maxPerComment {
			if builder.Len() > len(header) {
				bodies = append(bodies, ensureTrailingNewline(builder.String()))
				builder.Reset()
				builder.WriteString(header)
			}
		}

		builder.WriteString(chunk)
	}

	if builder.Len() > len(header) {
		bodies = append(bodies, ensureTrailingNewline(builder.String()))
	}

	if len(bodies) == 0 {
		bodies = append(bodies, ensureTrailingNewline(header))
	}

	return bodies
}

func includeNonEmptyChunks(chunks []string) []string {
	result := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		if strings.TrimSpace(chunk) == "" {
			continue
		}
		result = append(result, chunk)
	}
	return result
}

func ensureTrailingNewline(body string) string {
	body = strings.TrimRight(body, "\n") + "\n"
	return body
}

func sanitizeDiffHeaders(section, fileName, diff string) string {
	lines := strings.Split(diff, "\n")
	if len(lines) >= 2 && strings.HasPrefix(lines[0], "--- ") && strings.HasPrefix(lines[1], "+++ ") {
		label := sanitizeDiffFileName(fileName)
		switch section {
		case "Added":
			lines[0] = "--- /dev/null"
			lines[1] = fmt.Sprintf("+++ b/%s", label)
		case "Removed":
			lines[0] = fmt.Sprintf("--- a/%s", label)
			lines[1] = "+++ /dev/null"
		default:
			lines[0] = fmt.Sprintf("--- a/%s", label)
			lines[1] = fmt.Sprintf("+++ b/%s", label)
		}
	}

	return strings.Join(lines, "\n")
}

func sanitizeDiffFileName(name string) string {
	if name == "" {
		return "manifest"
	}
	return strings.ReplaceAll(name, " ", "_")
}

func isCRDManifest(entry DiffOutput) bool {
	name := strings.ToLower(strings.Trim(entry.File.Name, "/"))
	if strings.Contains(name, "crd") || strings.Contains(name, "customresourcedefinition") {
		return true
	}

	diffLower := strings.ToLower(entry.Diff)
	return strings.Contains(diffLower, "kind: customresourcedefinition")
}
