// Package filters implements composable output filters: ordinary tools that
// also double as post-processing steps other tools can pipeline through.
package filters

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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
