package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/shini4i/argo-compare/cmd/argo-compare/utils/logger"

	"github.com/shini4i/argo-compare/internal/comment"
	"github.com/shini4i/argo-compare/internal/ports"
)

// CommentStrategy delivers comparison results to an upstream comment system.
type CommentStrategy struct {
	Log             *logger.Logger
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
	// crowdedNoteDivisor halves a note's budget: once to cap a preamble that would
	// fill a comment on its own, and again as the diff budget if that preamble or a
	// continuation line would still consume the whole chunk.
	crowdedNoteDivisor = 2
	crdNoticeTemplate  = "> CRD manifest `%s` detected in the %s section. Diff omitted to keep merge request comments concise. Review the job logs for full details.\n"
)

// commentSection is one Application's contribution to a run's comment. A run
// comparing several Applications — an ApplicationSet generating them, or several
// changed manifests — collects one section each and publishes them together.
type commentSection struct {
	label  string
	result ComparisonResult
}

// Present formats one Application's comparison and pushes it as one or more
// comments depending on size.
// The context is used for cancellation and timeout control when posting comments.
func (s CommentStrategy) Present(ctx context.Context, result ComparisonResult) error {
	return s.PresentSections(ctx, []commentSection{{label: s.ApplicationPath, result: result}})
}

// PresentSections publishes every collected section as a single comment,
// splitting into further comments only when GitLab's note limit demands it. An
// empty batch posts nothing.
func (s CommentStrategy) PresentSections(ctx context.Context, sections []commentSection) error {
	if err := s.validate(); err != nil {
		return err
	}
	if len(sections) == 0 {
		return nil
	}

	bodies := buildCommentBodies(sections, s.ShowAdded, s.ShowRemoved)
	if err := s.postBodies(ctx, bodies); err != nil {
		return err
	}

	s.logSections(sections, len(bodies))
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

func (s CommentStrategy) postBodies(ctx context.Context, bodies []string) error {
	for idx, body := range bodies {
		bodyToPost := body
		if len(bodies) > 1 {
			bodyToPost = ensureTrailingNewline(strings.TrimRight(body, "\n") + fmt.Sprintf("\n\n_Part %d of %d_", idx+1, len(bodies)))
		}
		if err := s.Poster.Post(ctx, bodyToPost); err != nil {
			if len(bodies) > 1 {
				return fmt.Errorf("post diff comment (part %d/%d): %w", idx+1, len(bodies), err)
			}
			return fmt.Errorf("post diff comment: %w", err)
		}
	}
	return nil
}

func (s CommentStrategy) logSections(sections []commentSection, commentCount int) {
	subject := sectionLabel(sections[0].label)
	if len(sections) > 1 {
		subject = fmt.Sprintf("%d Applications", len(sections))
	}

	switch {
	case allSectionsEmpty(sections):
		s.Log.Infof("Posted comment summarizing absence of manifest changes for %s", subject)
	case commentCount > 1:
		s.Log.Infof("Posted %d comments with manifest diff summary for %s", commentCount, subject)
	default:
		s.Log.Infof("Posted comment with manifest diff summary for %s", subject)
	}
}

func allSectionsEmpty(sections []commentSection) bool {
	for _, section := range sections {
		if !section.result.IsEmpty() {
			return false
		}
	}

	return true
}

// sectionLabel names an Application for a comment, falling back when the
// caller had no path to give.
func sectionLabel(label string) string {
	if trimmed := strings.TrimSpace(label); trimmed != "" {
		return trimmed
	}

	return "unknown"
}

// buildCommentBodies renders every section under one run header, then packs the
// result into as few comments as GitLab's note limit allows.
func buildCommentBodies(sections []commentSection, showAdded, showRemoved bool) []string {
	const runHeader = "## Argo Compare Results\n\n"

	// computeMaxPerComment guarantees room beyond the fixed run header.
	maxChunkLen := computeMaxPerComment(len(runHeader)) - len(runHeader)

	chunks := make([]commentChunk, 0, len(sections))
	for _, section := range sections {
		chunks = append(chunks, sectionChunks(section, showAdded, showRemoved, maxChunkLen)...)
	}

	return assembleCommentBodies(runHeader, chunks)
}

// commentChunk is one renderable piece of a comment together with the
// Application it belongs to, so a comment starting partway through that
// Application can still name it. opensSection marks the piece that already
// carries the Application's own heading.
type commentChunk struct {
	label        string
	text         string
	opensSection bool
}

// continuationLine re-states an Application when its diffs spill into a
// further comment.
func continuationLine(label string) string {
	return fmt.Sprintf("**Application:** `%s` _(continued)_\n\n", escapeCommentLabel(label))
}

func escapeCommentLabel(label string) string {
	return strings.ReplaceAll(sectionLabel(label), "`", "\\`")
}

// preambleTruncationNotice replaces the tail of a validation summary too large
// to publish, pointing at the job log for the rest.
const preambleTruncationNotice = "> The rest of this summary was omitted to keep the comment within GitLab's size limit. Review the job logs for full details.\n\n"

// truncatePreamble bounds a section's opening at limit, cutting on a line
// boundary. A validation summary has one entry per failing resource and no cap,
// and a comment that exceeds GitLab's limit is rejected outright — which with
// batching would discard every other Application's comment too.
func truncatePreamble(preamble string, limit int) string {
	if len(preamble) <= limit {
		return preamble
	}

	keep := max(limit-len(preambleTruncationNotice), 0)
	if cut := strings.LastIndex(preamble[:keep], "\n"); cut > 0 {
		keep = cut
	}

	return strings.TrimRight(preamble[:keep], "\n") + "\n\n" + preambleTruncationNotice
}

// sectionChunks renders one Application: its preamble (label, validation,
// summary) followed by its diffs, or the no-differences note when it has none.
// The preamble rides along with the first diff so the two never land in
// separate comments.
func sectionChunks(section commentSection, showAdded, showRemoved bool, maxChunkLen int) []commentChunk {
	label := sectionLabel(section.label)

	var preamble strings.Builder
	fmt.Fprintf(&preamble, "**Application:** `%s`\n\n", escapeCommentLabel(label))

	if validationSummary := buildValidationSummary(section.result.ValidationResults); validationSummary != "" {
		preamble.WriteString(validationSummary)
	}
	if summary := buildSummaryLines(section.result, showAdded, showRemoved); summary != "" {
		preamble.WriteString(summary)
	}

	// A validation summary grows with the number of failing resources, so an
	// unbounded preamble could produce a comment GitLab rejects — abandoning
	// every other Application's comment queued behind it.
	preambleText := truncatePreamble(preamble.String(), maxChunkLen/crowdedNoteDivisor)

	if section.result.IsEmpty() {
		return []commentChunk{{
			label:        label,
			text:         preambleText + "No manifest differences detected :white_check_mark:\n\n",
			opensSection: true,
		}}
	}

	// Whichever opening is longer bounds the diffs, since a later comment
	// re-states the Application in place of the full preamble.
	reserved := max(len(preambleText), len(continuationLine(label)))
	available := maxChunkLen - reserved
	if available <= 0 {
		available = maxChunkLen / crowdedNoteDivisor
	}

	diffs, notices := collectDiffChunks(section.result, showAdded, showRemoved, available)
	diffs = append(diffs, buildCRDNotes(notices, available)...)

	chunks := make([]commentChunk, 0, len(diffs))
	for idx, diff := range diffs {
		if idx == 0 {
			chunks = append(chunks, commentChunk{label: label, text: preambleText + diff, opensSection: true})
			continue
		}
		chunks = append(chunks, commentChunk{label: label, text: diff})
	}

	if len(chunks) == 0 {
		chunks = append(chunks, commentChunk{label: label, text: preambleText, opensSection: true})
	}

	return chunks
}

// buildCRDNotes packs the notices for omitted diffs into chunks that fit a
// comment. One Application can omit arbitrarily many CRDs, and a chunk is never
// split further, so gathering them all into one would produce a comment GitLab
// rejects — discarding every Application queued behind it.
func buildCRDNotes(notices []string, limit int) []string {
	const heading = "**CRD Notes**\n"

	var (
		chunks  []string
		builder strings.Builder
	)
	builder.WriteString(heading)

	for _, notice := range notices {
		line := ensureTrailingNewline(notice)
		if builder.Len()+len(line) > limit && builder.Len() > len(heading) {
			chunks = append(chunks, builder.String()+"\n")
			builder.Reset()
			builder.WriteString(heading)
		}
		builder.WriteString(line)
	}

	if builder.Len() > len(heading) {
		chunks = append(chunks, builder.String()+"\n")
	}

	return chunks
}

// escapeInlineMarkdown sanitizes a string for safe interpolation into Markdown.
// Backslashes are escaped first so that subsequent backtick escaping cannot
// accidentally create a CommonMark backslash-escape sequence that leaves the
// backtick unescaped (e.g. a trailing \ before a ` would otherwise
// produce \\` which the renderer interprets as literal \ + open code-span).
// Newlines and carriage returns are collapsed to spaces to keep each bullet on
// a single line.
func escapeInlineMarkdown(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "`", "\\`")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

// buildValidationSummary formats validation results for a GitLab comment in a stable order.
// Each failing resource renders as a parent bullet (with cleaned filename when available)
// followed by one nested sub-bullet per non-empty line of the kubeconform message — keeping
// individual schema failures visually distinct instead of squashing them onto one line.
func buildValidationSummary(results map[string]ports.ValidationResult) string {
	if len(results) == 0 {
		return ""
	}

	keys := make([]string, 0, len(results))
	for k := range results {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var lines []string
	lines = append(lines, "**Validation**")

	for _, target := range keys {
		result := results[target]
		if result.InvocationError != "" {
			lines = append(lines, fmt.Sprintf("- ✗ validator could not run: %s", escapeInlineMarkdown(result.InvocationError)))
			continue
		}
		status := "✓"
		if !result.Valid {
			status = "✗"
		}
		lines = append(lines, fmt.Sprintf("- %s %d/%d valid", status, result.ResourceCount-result.ErrorCount, result.ResourceCount))
		for _, err := range result.Errors {
			issues := formatValidationIssues(err.Message)
			lines = append(lines, buildResourceHeader(err, len(issues) > 0))
			for _, issue := range issues {
				lines = append(lines, "    - "+escapeInlineMarkdown(issue))
			}
		}
	}

	return strings.Join(lines, "\n") + "\n\n"
}

// buildResourceHeader formats the parent bullet for a single failing resource.
// hasIssues controls the trailing punctuation: a colon when sub-bullets will
// follow, or a "(no message)" sentinel so an empty message doesn't render as a
// dangling colon.
func buildResourceHeader(err ports.ValidationError, hasIssues bool) string {
	kindName := fmt.Sprintf("`%s.%s`",
		escapeInlineMarkdown(err.Kind),
		escapeInlineMarkdown(err.Name))

	if err.Filename == "" {
		if hasIssues {
			return "  - " + kindName + ":"
		}
		return "  - " + kindName + " (no message)"
	}

	fname := fmt.Sprintf("`%s`", escapeInlineMarkdown(err.Filename))
	if hasIssues {
		return "  - " + kindName + " — " + fname + ":"
	}
	return "  - " + kindName + " — " + fname + " (no message)"
}

// formatValidationIssues splits a kubeconform message into one entry per
// non-empty line. Kubeconform packs multiple schema failures for a single
// resource into one msg field separated by newlines; rendering them as
// sub-bullets keeps each issue individually scannable in the MR comment.
// Returns nil when message is empty or contains only whitespace lines.
func formatValidationIssues(message string) []string {
	var out []string
	for _, line := range strings.Split(message, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
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

// assembleCommentBodies packs chunks into as few comments as the note limit
// allows. A comment that starts partway through an Application repeats that
// Application's name, so no diff is ever shown without saying whose it is.
func assembleCommentBodies(header string, chunks []commentChunk) []string {
	maxPerComment := computeMaxPerComment(len(header))

	var bodies []string
	var builder strings.Builder
	builder.WriteString(header)
	atCommentStart := true

	for _, chunk := range includeNonEmptyChunks(chunks) {
		if builder.Len()+len(chunk.text) > maxPerComment && builder.Len() > len(header) {
			bodies = append(bodies, ensureTrailingNewline(builder.String()))
			builder.Reset()
			builder.WriteString(header)
			atCommentStart = true
		}

		if atCommentStart && !chunk.opensSection {
			builder.WriteString(continuationLine(chunk.label))
		}

		builder.WriteString(chunk.text)
		atCommentStart = false
	}

	if builder.Len() > len(header) {
		bodies = append(bodies, ensureTrailingNewline(builder.String()))
	}

	if len(bodies) == 0 {
		bodies = append(bodies, ensureTrailingNewline(header))
	}

	return bodies
}

func includeNonEmptyChunks(chunks []commentChunk) []commentChunk {
	result := make([]commentChunk, 0, len(chunks))
	for _, chunk := range chunks {
		if strings.TrimSpace(chunk.text) == "" {
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

// isCRDManifest reports whether the provided diff output appears to describe a
// CustomResourceDefinition manifest by inspecting both the path and diff
// content.
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
	for idx, segment := range segments {
		if segment == "" {
			continue
		}
		if segment == "crd" || segment == "crds" {
			return true
		}
		isLastSegment := idx == len(segments)-1
		if isLastSegment && hasCRDFilenamePattern(segment) {
			return true
		}
	}

	return false
}

// hasCRDFilenamePattern reports whether a file name follows common CRD manifest conventions.
func hasCRDFilenamePattern(segment string) bool {
	if segment == "" {
		return false
	}

	lowered := strings.ToLower(segment)
	if strings.Contains(lowered, ".crd.") {
		return true
	}

	crdSuffixes := []string{
		"crd.yaml",
		"crd.yml",
		"-crd.yaml",
		"-crd.yml",
		"_crd.yaml",
		"_crd.yml",
	}

	for _, suffix := range crdSuffixes {
		if strings.HasSuffix(lowered, suffix) {
			return true
		}
	}

	return false
}
