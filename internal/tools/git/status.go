package git

import "encoding/json"

type StatusTool struct{ gitTool }

func init() {}

func NewStatusTool() *StatusTool {
	return &StatusTool{gitTool{name: "git_status", description: "Returns git status in porcelain format", schema: json.RawMessage(`{"type":"object","properties":{}}`)}}
}

func (t *StatusTool) Execute(args json.RawMessage) (string, error) {
	return runGit("status", "--porcelain=v1", "-b")
}
