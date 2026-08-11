package filters

import (
	"encoding/json"
	"errors"
	"strings"
)

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
