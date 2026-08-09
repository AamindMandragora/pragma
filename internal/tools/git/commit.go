package git

import (
	"encoding/json"
	"fmt"

	"github.com/AamindMandragora/pragma/internal/tools"
)

type CommitTool struct{ gitTool }

func NewCommitTool() *CommitTool {
	return &CommitTool{gitTool{name: "git_commit", description: "Stages and commits changes", schema: json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"},"files":{"type":"array","items":{"type":"string"}}},"required":["message"]}`), access: tools.AccessWrite}}
}

func (t *CommitTool) ConfirmSummary(args json.RawMessage) string {
	var params struct {
		Message string   `json:"message"`
		Files   []string `json:"files,omitempty"`
	}
	_ = json.Unmarshal(args, &params)
	if len(params.Files) > 0 {
		return fmt.Sprintf("commit %q (%d files)", params.Message, len(params.Files))
	}
	return fmt.Sprintf("commit %q", params.Message)
}

func (t *CommitTool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		Message string   `json:"message"`
		Files   []string `json:"files,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	if err := required("message", params.Message); err != nil {
		return "", err
	}
	if len(params.Files) > 0 {
		if _, err := runGit(append([]string{"add", "--"}, params.Files...)...); err != nil {
			return "", err
		}
	} else {
		if _, err := runGit("add", "-A"); err != nil {
			return "", err
		}
	}
	return runGit("commit", "-m", params.Message)
}
