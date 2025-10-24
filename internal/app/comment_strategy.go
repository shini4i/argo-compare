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
	// gitlabNoteLengthLimit reflects GitLab's documented 1 MB limit for note bodies.
	gitlabNoteLengthLimit = 1_000_000
	// commentPartReserve keeps room for part numbering suffixes when chunking comments.
	commentPartReserve = 32
	crdNoticeTemplate  = "> CRD manifest `%s` detected in the %s section. Diff omitted to keep merge request comments concise. Review the job logs for full details.\n"
)

// Present formats comparison results and pushes them as one or more comments depending on size.
func (s CommentStrategy) Present(result ComparisonResult) error {
	if err := s.validate(); err != nil {
		return err
	}

	bodies := buildCommentBodies(result, s.ShowAdded, s.ShowRemoved, s.ApplicationPath)
	if err := s.postBodies(bodies); err != nil {
		return err
	}

	s.logResult(result, len(bodies))
	return nil
}

func (s CommentStrategy) validate() error {
	if s.Poster == nil {
		return errors.New("comment strategy requires a poster implementation")
	}
	if s.Log == nil {
		return errors.New("comment strategy requires a logger")
	}
	return nil
}

func (s CommentStrategy) postBodies(bodies []string) error {
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
	return nil
}

func (s CommentStrategy) logResult(result ComparisonResult, commentCount int) {
	app := strings.TrimSpace(s.ApplicationPath)
	if app == "" {
		app = "unknown application"
	}

	switch {
	case result.IsEmpty():
		s.Log.Infof("Posted comment summarizing absence of manifest changes for %s", app)
	case commentCount > 1:
		s.Log.Infof("Posted %d comments with manifest diff summary for %s", commentCount, app)
	default:
		s.Log.Infof("Posted comment with manifest diff summary for %s", app)
	}
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

	maxPerComment := computeMaxPerComment(len(header))

	maxChunkLen := maxPerComment - len(header)
	if maxChunkLen <= 0 {
		maxChunkLen = maxPerComment / 2
	}

	chunks, notices := collectDiffChunks(result, showAdded, showRemoved, maxChunkLen)
	if len(notices) > 0 {
		var noticeBuilder strings.Builder
		noticeBuilder.WriteString("**CRD Notes**\n")
		for _, notice := range notices {
			noticeBuilder.WriteString(notice)
			if !strings.HasSuffix(notice, "\n") {
				noticeBuilder.WriteString("\n")
			}
		}
		noticeBuilder.WriteString("\n")
		chunks = append(chunks, noticeBuilder.String())
	}

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

// collectDiffChunks flattens diff outputs into renderable chunks and gathers notices for omitted sections (e.g. CRDs).
func collectDiffChunks(result ComparisonResult, showAdded, showRemoved bool, maxChunkLen int) ([]string, []string) {
	var (
		chunks  []string
		notices []string
	)

	if showAdded {
		addedChunks, addedNotices := buildDiffChunks("Added", result.Added, maxChunkLen)
		chunks = append(chunks, addedChunks...)
		notices = append(notices, addedNotices...)
	} else if len(result.Added) > 0 {
		chunks = append(chunks, buildOmittedNotice("Added", len(result.Added)))
	}

	if showRemoved {
		removedChunks, removedNotices := buildDiffChunks("Removed", result.Removed, maxChunkLen)
		chunks = append(chunks, removedChunks...)
		notices = append(notices, removedNotices...)
	} else if len(result.Removed) > 0 {
		chunks = append(chunks, buildOmittedNotice("Removed", len(result.Removed)))
	}

	changedChunks, changedNotices := buildDiffChunks("Changed", result.Changed, maxChunkLen)
	chunks = append(chunks, changedChunks...)
	notices = append(notices, changedNotices...)

	return chunks, notices
}

func buildOmittedNotice(section string, count int) string {
	return fmt.Sprintf("> %s manifests (%d) are present but not shown with the current settings.\n\n", section, count)
}

// buildDiffChunks produces diff chunks for a single section (Added/Removed/Changed) and returns any notices.
func buildDiffChunks(section string, entries []DiffOutput, maxChunkLen int) ([]string, []string) {
	var (
		chunks  []string
		notices []string
	)
	for _, entry := range entries {
		entryChunks, notice := buildDiffEntryChunks(section, entry, maxChunkLen)
		if notice != "" {
			notices = append(notices, notice)
		}
		chunks = append(chunks, entryChunks...)
	}
	return chunks, notices
}

// buildDiffEntryChunks formats a single diff entry into one or more chunks, returning the diff text and optional notice.
func buildDiffEntryChunks(section string, entry DiffOutput, maxChunkLen int) ([]string, string) {
	fileName := strings.TrimPrefix(entry.File.Name, "/")
	if fileName == "" {
		fileName = "unknown"
	}

	diff := strings.TrimRight(entry.Diff, "\n")
	if diff == "" {
		diff = "(no diff output)"
	}

	if isCRDManifest(entry) {
		notice := fmt.Sprintf(crdNoticeTemplate, fileName, strings.ToLower(section))
		return nil, notice
	}

	diff = stripDiffHeaders(diff)

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

	return chunks, ""
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
	maxPerComment := computeMaxPerComment(len(header))

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

func computeMaxPerComment(headerLen int) int {
	maxPerComment := gitlabNoteLengthLimit - commentPartReserve
	if maxPerComment <= 0 {
		maxPerComment = gitlabNoteLengthLimit
	}
	if headerLen >= maxPerComment {
		maxPerComment = headerLen + 1
	}
	return maxPerComment
}

// stripDiffHeaders removes git metadata headers from diff output, leaving only the hunk details.
func stripDiffHeaders(diff string) string {
	lines := strings.Split(diff, "\n")
	start := 0
	for start < len(lines) {
		line := lines[start]
		if strings.HasPrefix(line, "diff --git ") ||
			strings.HasPrefix(line, "index ") ||
			strings.HasPrefix(line, "--- ") ||
			strings.HasPrefix(line, "+++ ") {
			start++
			continue
		}
		break
	}

	if start >= len(lines) {
		return ""
	}
	return strings.Join(lines[start:], "\n")
}

func isCRDManifest(entry DiffOutput) bool {
	name := strings.ToLower(strings.Trim(entry.File.Name, "/"))
	if hasCRDPathIndicator(name) {
		return true
	}

	diffLower := strings.ToLower(entry.Diff)
	return strings.Contains(diffLower, "kind: customresourcedefinition")
}

// hasCRDPathIndicator reports whether the path strongly suggests a CRD manifest.
func hasCRDPathIndicator(name string) bool {
	if name == "" {
		return false
	}

	segments := strings.Split(name, "/")
	for _, segment := range segments {
		if segment == "crds" {
			return true
		}
		if strings.HasSuffix(segment, ".crd.yaml") || strings.HasSuffix(segment, "-crd.yaml") {
			return true
		}
	}

	return false
}
