package filters

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

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
