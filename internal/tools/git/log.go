package git

import (
	"encoding/json"
	"fmt"
)

type LogTool struct{ gitTool }

func NewLogTool() *LogTool {
	return &LogTool{gitTool{name: "git_log", description: "Returns previous commits limited to an optional n", schema: json.RawMessage(`{"type":"object","properties":{"n":{"type":"integer"}}}`)}}
}

func (t *LogTool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		N int `json:"n,omitempty"`
	}
	_ = json.Unmarshal(args, &params)
	if params.N <= 0 {
		params.N = 10
	}
	return runGit("log", fmt.Sprintf("-%d", params.N), "--format=%H%x09%an%x09%ad%x09%s", "--date=iso-strict")
}
