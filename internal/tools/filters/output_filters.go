package filters

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/AamindMandragora/pragma/internal/llm"
	"github.com/AamindMandragora/pragma/internal/tools"
)

const (
	defaultHeadLines        = 20
	defaultTailLines        = 20
	defaultIssueMatches     = 200
	defaultCompactBlanks    = 1
	defaultDedupeRepeats    = 1
	defaultSummaryMaxTokens = 1024
	maxSummaryChunkChars    = 30000
	defaultIssueExpression  = `(?i)\b(error|errors|warning|warnings|fatal|panic|traceback|exception|failed|failure|fail|critical|assertion|undefined|deprecated)\b`
)

// RegisterBuiltinOutputFilters adds the filters that are useful across every
// tool. They are also ordinary tools, so a filter can be called directly with
// {"text":"..."} when a pipeline is inconvenient.
func RegisterBuiltinOutputFilters(registry *tools.Registry, model *llm.Model) {
	registry.RegisterFilter(&SummarizeOutputFilter{Model: model})
	registry.RegisterFilter(&FindIssuesFilter{})
	registry.RegisterFilter(&HeadOutputFilter{})
	registry.RegisterFilter(&TailOutputFilter{})
	registry.RegisterFilter(&LineRangeFilter{})
	registry.RegisterFilter(&GrepOutputFilter{})
	registry.RegisterFilter(&CompactOutputFilter{})
	registry.RegisterFilter(&DedupeOutputFilter{})
	registry.RegisterFilter(&JSONPrettyFilter{})
	registry.RegisterFilter(&OutputStatsFilter{})
}

type textInput struct {
	Text string `json:"text,omitempty"`
}

func directText(args json.RawMessage) (string, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(args, &fields); err != nil {
		return "", err
	}
	rawText, ok := fields["text"]
	if !ok || string(rawText) == "null" {
		return "", errors.New("text is required")
	}
	var params textInput
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	return params.Text, nil
}

func textFilterSchema(description string, extra string) json.RawMessage {
	if description == "" {
		description = "Complete tool output to filter."
	}
	return json.RawMessage(fmt.Sprintf(`{"type":"object","properties":{"text":{"type":"string","description":%q}%s},"required":["text"]}`, description, extra))
}

func outputLines(text string) ([]string, bool) {
	if text == "" {
		return nil, false
	}
	hadTrailingNewline := strings.HasSuffix(text, "\n")
	lines := strings.Split(text, "\n")
	if hadTrailingNewline {
		lines = lines[:len(lines)-1]
	}
	return lines, hadTrailingNewline
}

func joinOutputLines(lines []string, trailingNewline bool) string {
	if len(lines) == 0 {
		return ""
	}
	result := strings.Join(lines, "\n")
	if trailingNewline {
		result += "\n"
	}
	return result
}

func parsePositiveInt(args json.RawMessage, field string, defaultValue int) (int, error) {
	var params map[string]json.RawMessage
	if err := json.Unmarshal(args, &params); err != nil {
		return 0, err
	}
	value, ok := params[field]
	if !ok || string(value) == "null" {
		return defaultValue, nil
	}
	var n int
	if err := json.Unmarshal(value, &n); err != nil {
		return 0, fmt.Errorf("%s must be an integer", field)
	}
	if n <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", field)
	}
	return n, nil
}

func selectLines(text string, start, end int) (string, error) {
	lines, trailing := outputLines(text)
	if len(lines) == 0 {
		return "", nil
	}
	if start < 1 || end < start {
		return "", errors.New("line range must have start >= 1 and end >= start")
	}
	if start > len(lines) {
		return "", fmt.Errorf("start line %d is beyond output (%d lines)", start, len(lines))
	}
	if end > len(lines) {
		end = len(lines)
	}
	return joinOutputLines(lines[start-1:end], trailing || end < len(lines)), nil
}

type HeadOutputFilter struct{}

func (f *HeadOutputFilter) Name() string { return "head" }
func (f *HeadOutputFilter) Description() string {
	return "Output filter: returns the first n lines (default 20). Use in filters:[{name: head, args: {n: 10}}]."
}
func (f *HeadOutputFilter) Schema() json.RawMessage {
	return textFilterSchema("", `,"n":{"type":"integer","minimum":1,"default":20,"description":"Number of lines to keep."}`)
}
func (f *HeadOutputFilter) Execute(args json.RawMessage) (string, error) {
	text, err := directText(args)
	if err != nil {
		return "", err
	}
	return f.Apply(text, args)
}
func (f *HeadOutputFilter) Apply(text string, args json.RawMessage) (string, error) {
	n, err := parsePositiveInt(args, "n", defaultHeadLines)
	if err != nil {
		return "", err
	}
	lines, trailing := outputLines(text)
	if n > len(lines) {
		n = len(lines)
	}
	return joinOutputLines(lines[:n], trailing || n < len(lines)), nil
}

type TailOutputFilter struct{}

func (f *TailOutputFilter) Name() string { return "tail" }
func (f *TailOutputFilter) Description() string {
	return "Output filter: returns the last n lines (default 20). Use in filters:[{name: tail, args: {n: 50}}]."
}
func (f *TailOutputFilter) Schema() json.RawMessage {
	return textFilterSchema("", `,"n":{"type":"integer","minimum":1,"default":20,"description":"Number of lines to keep."}`)
}
func (f *TailOutputFilter) Execute(args json.RawMessage) (string, error) {
	text, err := directText(args)
	if err != nil {
		return "", err
	}
	return f.Apply(text, args)
}
func (f *TailOutputFilter) Apply(text string, args json.RawMessage) (string, error) {
	n, err := parsePositiveInt(args, "n", defaultTailLines)
	if err != nil {
		return "", err
	}
	lines, trailing := outputLines(text)
	if n > len(lines) {
		n = len(lines)
	}
	return joinOutputLines(lines[len(lines)-n:], trailing), nil
}

type LineRangeFilter struct{}

func (f *LineRangeFilter) Name() string { return "line_range" }
func (f *LineRangeFilter) Description() string {
	return "Output filter: keeps an inclusive line range. Useful for extracting a precise section from any tool output."
}
func (f *LineRangeFilter) Schema() json.RawMessage {
	return textFilterSchema("", `,"start_line":{"type":"integer","minimum":1,"description":"First line, inclusive."},"end_line":{"type":"integer","minimum":1,"description":"Last line, inclusive; defaults to the end."}`)
}
func (f *LineRangeFilter) Execute(args json.RawMessage) (string, error) {
	text, err := directText(args)
	if err != nil {
		return "", err
	}
	return f.Apply(text, args)
}
func (f *LineRangeFilter) Apply(text string, args json.RawMessage) (string, error) {
	var params struct {
		Start int `json:"start_line,omitempty"`
		End   int `json:"end_line,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(args, &fields); err != nil {
		return "", err
	}
	start, ok := fields["start_line"]
	if !ok || string(start) == "null" {
		return "", errors.New("start_line is required")
	}
	if params.Start < 1 {
		return "", errors.New("start_line must be at least 1")
	}
	lines, _ := outputLines(text)
	end := len(lines)
	if rawEnd, ok := fields["end_line"]; ok && string(rawEnd) != "null" {
		if params.End < 1 {
			return "", errors.New("end_line must be at least 1")
		}
		end = params.End
	}
	return selectLines(text, params.Start, end)
}

type FindIssuesFilter struct{}

func (f *FindIssuesFilter) Name() string { return "find_issues" }
func (f *FindIssuesFilter) Description() string {
	return "Output filter: finds likely errors, warnings, failures, panics, tracebacks, and related diagnostic lines, with optional context."
}
func (f *FindIssuesFilter) Schema() json.RawMessage {
	return textFilterSchema("", `,"pattern":{"type":"string","description":"Optional regular expression; defaults to common error and warning words."},"context":{"type":"integer","minimum":0,"default":0,"description":"Neighboring lines to include before and after each match."},"max_matches":{"type":"integer","minimum":1,"default":200}`)
}
func (f *FindIssuesFilter) Execute(args json.RawMessage) (string, error) {
	text, err := directText(args)
	if err != nil {
		return "", err
	}
	return f.Apply(text, args)
}
func (f *FindIssuesFilter) Apply(text string, args json.RawMessage) (string, error) {
	return findMatchingLines(text, args, defaultIssueExpression, true)
}

type GrepOutputFilter struct{}

func (f *GrepOutputFilter) Name() string { return "grep" }
func (f *GrepOutputFilter) Description() string {
	return "Output filter: searches output with a regular expression, returning matching lines with source line numbers and optional context."
}
func (f *GrepOutputFilter) Schema() json.RawMessage {
	return textFilterSchema("", `,"pattern":{"type":"string","description":"Regular expression to search for."},"ignore_case":{"type":"boolean","default":true},"context":{"type":"integer","minimum":0,"default":0},"max_matches":{"type":"integer","minimum":1,"default":200}`)
}
func (f *GrepOutputFilter) Execute(args json.RawMessage) (string, error) {
	text, err := directText(args)
	if err != nil {
		return "", err
	}
	return f.Apply(text, args)
}
func (f *GrepOutputFilter) Apply(text string, args json.RawMessage) (string, error) {
	var params struct {
		Pattern string `json:"pattern"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	if params.Pattern == "" {
		return "", errors.New("pattern is required")
	}
	var options map[string]json.RawMessage
	if err := json.Unmarshal(args, &options); err != nil {
		return "", err
	}
	ignoreCase := true
	if raw, ok := options["ignore_case"]; ok {
		if err := json.Unmarshal(raw, &ignoreCase); err != nil {
			return "", errors.New("ignore_case must be a boolean")
		}
	}
	pattern := params.Pattern
	if ignoreCase {
		pattern = `(?i)` + pattern
	}
	options["pattern"], _ = json.Marshal(pattern)
	searchArgs, err := json.Marshal(options)
	if err != nil {
		return "", err
	}
	return findMatchingLines(text, searchArgs, pattern, false)
}

func findMatchingLines(text string, args json.RawMessage, defaultPattern string, issueMode bool) (string, error) {
	pattern := defaultPattern
	var params struct {
		Pattern string `json:"pattern"`
		Context int    `json:"context,omitempty"`
		Max     int    `json:"max_matches,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	if params.Pattern != "" {
		pattern = params.Pattern
	}
	if params.Context < 0 {
		return "", errors.New("context must not be negative")
	}
	maxMatches := params.Max
	if maxMatches == 0 {
		maxMatches = defaultIssueMatches
	}
	if maxMatches < 0 {
		return "", errors.New("max_matches must be greater than zero")
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid pattern: %w", err)
	}
	lines, _ := outputLines(text)
	matched := make([]int, 0)
	for i, line := range lines {
		if re.MatchString(line) {
			matched = append(matched, i)
			if len(matched) > maxMatches {
				break
			}
		}
	}
	if len(matched) == 0 {
		if issueMode {
			return "No error or warning lines found.", nil
		}
		return "No matching lines found.", nil
	}
	truncated := len(matched) > maxMatches
	if truncated {
		matched = matched[:maxMatches]
	}
	selected := make(map[int]bool)
	for _, index := range matched {
		start := index - params.Context
		if start < 0 {
			start = 0
		}
		end := index + params.Context
		if end >= len(lines) {
			end = len(lines) - 1
		}
		for i := start; i <= end; i++ {
			selected[i] = true
		}
	}
	indexes := make([]int, 0, len(selected))
	for index := range selected {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	result := make([]string, 0, len(indexes))
	for _, index := range indexes {
		result = append(result, fmt.Sprintf("[line %d] %s", index+1, lines[index]))
	}
	if truncated {
		result = append(result, fmt.Sprintf("... (showing first %d matches)", maxMatches))
	}
	return strings.Join(result, "\n"), nil
}

type CompactOutputFilter struct{}

func (f *CompactOutputFilter) Name() string { return "compact_output" }
func (f *CompactOutputFilter) Description() string {
	return "Output filter: removes repeated blank lines and trailing whitespace while preserving the useful content."
}
func (f *CompactOutputFilter) Schema() json.RawMessage {
	return textFilterSchema("", `,"max_blank_lines":{"type":"integer","minimum":0,"default":1},"trim_trailing_whitespace":{"type":"boolean","default":true}`)
}
func (f *CompactOutputFilter) Execute(args json.RawMessage) (string, error) {
	text, err := directText(args)
	if err != nil {
		return "", err
	}
	return f.Apply(text, args)
}
func (f *CompactOutputFilter) Apply(text string, args json.RawMessage) (string, error) {
	var params struct {
		MaxBlank int  `json:"max_blank_lines,omitempty"`
		Trim     bool `json:"trim_trailing_whitespace,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(args, &fields); err != nil {
		return "", err
	}
	maxBlank := defaultCompactBlanks
	if raw, ok := fields["max_blank_lines"]; ok && string(raw) != "null" {
		maxBlank = params.MaxBlank
	}
	if maxBlank < 0 {
		return "", errors.New("max_blank_lines must not be negative")
	}
	trim := true
	if raw, ok := fields["trim_trailing_whitespace"]; ok && string(raw) != "null" {
		trim = params.Trim
	}
	lines := strings.Split(text, "\n")
	result := make([]string, 0, len(lines))
	blankCount := 0
	for _, line := range lines {
		if trim {
			line = strings.TrimRight(line, " \t")
		}
		if strings.TrimSpace(line) == "" {
			blankCount++
			if blankCount > maxBlank {
				continue
			}
		} else {
			blankCount = 0
		}
		result = append(result, line)
	}
	return strings.Join(result, "\n"), nil
}

type DedupeOutputFilter struct{}

func (f *DedupeOutputFilter) Name() string { return "dedupe_output" }
func (f *DedupeOutputFilter) Description() string {
	return "Output filter: collapses consecutive duplicate lines, useful for noisy logs and repeated progress output."
}
func (f *DedupeOutputFilter) Schema() json.RawMessage {
	return textFilterSchema("", `,"max_repeats":{"type":"integer","minimum":1,"default":1,"description":"Maximum consecutive copies to keep."}`)
}
func (f *DedupeOutputFilter) Execute(args json.RawMessage) (string, error) {
	text, err := directText(args)
	if err != nil {
		return "", err
	}
	return f.Apply(text, args)
}
func (f *DedupeOutputFilter) Apply(text string, args json.RawMessage) (string, error) {
	maxRepeats, err := parsePositiveInt(args, "max_repeats", defaultDedupeRepeats)
	if err != nil {
		return "", err
	}
	lines, trailing := outputLines(text)
	if len(lines) == 0 {
		return "", nil
	}
	result := make([]string, 0, len(lines))
	previous := ""
	repeats := 0
	for _, line := range lines {
		if line == previous {
			repeats++
		} else {
			previous = line
			repeats = 1
		}
		if repeats <= maxRepeats {
			result = append(result, line)
		}
	}
	return joinOutputLines(result, trailing), nil
}

type JSONPrettyFilter struct{}

func (f *JSONPrettyFilter) Name() string { return "json_pretty" }
func (f *JSONPrettyFilter) Description() string {
	return "Output filter: validates and pretty-prints JSON output for easier inspection."
}
func (f *JSONPrettyFilter) Schema() json.RawMessage {
	return textFilterSchema("", `,"indent":{"type":"string","default":"  ","description":"Indentation string."}`)
}
func (f *JSONPrettyFilter) Execute(args json.RawMessage) (string, error) {
	text, err := directText(args)
	if err != nil {
		return "", err
	}
	return f.Apply(text, args)
}
func (f *JSONPrettyFilter) Apply(text string, args json.RawMessage) (string, error) {
	indent := "  "
	var params struct {
		Indent string `json:"indent,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	if params.Indent != "" {
		indent = params.Indent
	}
	var result bytes.Buffer
	if err := json.Indent(&result, []byte(text), "", indent); err != nil {
		return "", fmt.Errorf("invalid JSON: %w", err)
	}
	return result.String(), nil
}

type OutputStatsFilter struct{}

func (f *OutputStatsFilter) Name() string { return "output_stats" }
func (f *OutputStatsFilter) Description() string {
	return "Output filter: reports output size, line counts, and common diagnostic counts instead of returning the content."
}
func (f *OutputStatsFilter) Schema() json.RawMessage {
	return textFilterSchema("", ``)
}
func (f *OutputStatsFilter) Execute(args json.RawMessage) (string, error) {
	text, err := directText(args)
	if err != nil {
		return "", err
	}
	return f.Apply(text, args)
}
func (f *OutputStatsFilter) Apply(text string, _ json.RawMessage) (string, error) {
	lines, _ := outputLines(text)
	nonEmpty := 0
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			nonEmpty++
		}
	}
	issues, err := regexp.Compile(defaultIssueExpression)
	if err != nil {
		return "", err
	}
	issueLines := 0
	for _, line := range lines {
		if issues.MatchString(line) {
			issueLines++
		}
	}
	return fmt.Sprintf("bytes: %d\ncharacters: %d\nlines: %d\nnon_empty_lines: %d\nblank_lines: %d\nissue_lines: %d", len([]byte(text)), utf8.RuneCountInString(text), len(lines), nonEmpty, len(lines)-nonEmpty, issueLines), nil
}

type SummarizeOutputFilter struct {
	Model *llm.Model
}

func (f *SummarizeOutputFilter) Name() string { return "summarize_output" }
func (f *SummarizeOutputFilter) Description() string {
	return "Output filter: privately summarizes complete tool output, preserving important errors, names, behavior, and actionable details."
}
func (f *SummarizeOutputFilter) Schema() json.RawMessage {
	return textFilterSchema("", `,"focus":{"type":"string","description":"Optional aspect to focus on."}`)
}
func (f *SummarizeOutputFilter) Execute(args json.RawMessage) (string, error) {
	text, err := directText(args)
	if err != nil {
		return "", err
	}
	return f.Apply(text, args)
}
func (f *SummarizeOutputFilter) Apply(text string, args json.RawMessage) (string, error) {
	var params struct {
		Focus string `json:"focus,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	if strings.TrimSpace(text) == "" {
		return "", errors.New("cannot summarize empty output")
	}
	if f.Model == nil || f.Model.Provider == nil {
		return "", errors.New("summarize_output has no model provider")
	}
	return f.summarize(context.Background(), text, params.Focus)
}

func (f *SummarizeOutputFilter) summarize(ctx context.Context, text, focus string) (string, error) {
	chunks := splitSummaryChunks(text, maxSummaryChunkChars)
	partial := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		result, err := f.summarizeChunk(ctx, chunk, focus)
		if err != nil {
			return "", err
		}
		partial = append(partial, result)
	}
	if len(partial) == 1 {
		return partial[0], nil
	}

	combined := strings.Join(partial, "\n\n---\n\n")
	return f.summarizeChunk(ctx, combined, focus+" Combine the partial summaries into one concise result.")
}

func (f *SummarizeOutputFilter) summarizeChunk(ctx context.Context, text, focus string) (string, error) {
	model := *f.Model
	model.MaxTokens = defaultSummaryMaxTokens
	model.Effort = ""

	prompt := "Summarize the supplied text for a coding agent. Preserve important names, behavior, errors, interfaces, and actionable details. Do not reproduce the full source. Keep the result concise."
	if focus != "" {
		prompt += " Focus on: " + focus
	}
	prompt += "\n\nTEXT:\n" + text

	ch, err := model.Provider.Chat(ctx, []llm.Message{
		{Role: "system", Content: prompt},
		{Role: "user", Content: "Return only the summary."},
	}, nil, model)
	if err != nil {
		return "", err
	}

	var result strings.Builder
	for event := range ch {
		switch event.Type {
		case "text":
			result.WriteString(event.Text)
		case "error":
			return "", event.Err
		}
	}
	if strings.TrimSpace(result.String()) == "" {
		return "", errors.New("summarizer returned no visible response")
	}
	return strings.TrimSpace(result.String()), nil
}

func splitSummaryChunks(text string, maxChars int) []string {
	if len(text) <= maxChars {
		return []string{text}
	}

	var chunks []string
	for len(text) > maxChars {
		cut := strings.LastIndexAny(text[:maxChars], "\n ")
		if cut < maxChars/2 {
			cut = maxChars
		}
		chunks = append(chunks, text[:cut])
		text = strings.TrimLeft(text[cut:], " \n")
	}
	if text != "" {
		chunks = append(chunks, text)
	}
	return chunks
}
