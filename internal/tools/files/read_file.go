package files

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/AamindMandragora/pragma/internal/process"
)

type ReadFileTool struct{}

func (r *ReadFileTool) Name() string {
	return "read_file"
}

func (r *ReadFileTool) Description() string {
	return "Reads a complete file by path, or an inclusive 1-based line range with start_line/end_line. For large files, compose summarize_output or another output filter."
}

func (r *ReadFileTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Path to the file"},"start_line":{"type":"integer","minimum":1,"description":"Optional 1-based first line to return, inclusive."},"end_line":{"type":"integer","minimum":1,"description":"Optional 1-based last line to return, inclusive; defaults to the end of the file."}},"required":["path"]}`)
}

func (r *ReadFileTool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		Path      string `json:"path"`
		StartLine int    `json:"start_line,omitempty"`
		EndLine   int    `json:"end_line,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(args, &fields); err != nil {
		return "", err
	}
	if process.IsIgnored(params.Path) {
		return "", fmt.Errorf("access denied: %s is in .agentignore", params.Path)
	}
	contents, err := os.ReadFile(params.Path)
	if err != nil {
		return "", err
	}
	startLine, hasStart := fields["start_line"]
	endLine, hasEnd := fields["end_line"]
	if hasStart && string(startLine) != "null" && params.StartLine < 1 {
		return "", fmt.Errorf("start_line must be at least 1")
	}
	if hasEnd && string(endLine) != "null" && params.EndLine < 1 {
		return "", fmt.Errorf("end_line must be at least 1")
	}
	if (!hasStart || string(startLine) == "null") && (!hasEnd || string(endLine) == "null") {
		return string(contents), nil
	}
	return readLineRange(string(contents), params.StartLine, params.EndLine)
}

func readLineRange(contents string, startLine, endLine int) (string, error) {
	lines := strings.SplitAfter(contents, "\n")
	if len(lines) > 1 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	start := 1
	if startLine != 0 {
		start = startLine
	}
	if start < 1 {
		return "", fmt.Errorf("start_line must be at least 1")
	}
	if len(lines) == 0 {
		if start == 1 && (endLine == 0 || endLine >= 1) {
			return "", nil
		}
		return "", fmt.Errorf("start_line %d is beyond the empty file", start)
	}
	end := len(lines)
	if endLine != 0 {
		end = endLine
	}
	if end < start {
		return "", fmt.Errorf("end_line must be greater than or equal to start_line")
	}
	if start > len(lines) {
		return "", fmt.Errorf("start_line %d is beyond file (%d lines)", start, len(lines))
	}
	if end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[start-1:end], ""), nil
}
