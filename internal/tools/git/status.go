package git

import (
	"encoding/json"

	"github.com/AamindMandragora/pragma/internal/tools"
)

type StatusTool struct{ gitTool }

func NewStatusTool() *StatusTool {
	return &StatusTool{gitTool{name: "git_status", description: "Returns git status in porcelain format", schema: json.RawMessage(`{"type":"object","properties":{}}`), access: tools.AccessRead}}
}

func (t *StatusTool) Execute(args json.RawMessage) (string, error) {
	return runGit("status", "--porcelain=v1", "-b")
}
