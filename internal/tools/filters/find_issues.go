package filters

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

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
