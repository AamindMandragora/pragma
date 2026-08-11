package filters

import (
	"encoding/json"
	"errors"
)

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
