package git

import "encoding/json"

type StashTool struct{ gitTool }

func NewStashTool() *StashTool {
	return &StashTool{gitTool{name: "git_stash", description: "Pushes or pops git stash entries", schema: json.RawMessage(`{"type":"object","properties":{"action":{"type":"string"},"message":{"type":"string"}},"required":["action"]}`)}}
}

func (t *StashTool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		Action  string `json:"action"`
		Message string `json:"message,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	switch params.Action {
	case "push":
		if params.Message != "" {
			return runGit("stash", "push", "-m", params.Message)
		}
		return runGit("stash", "push")
	case "pop":
		return runGit("stash", "pop")
	default:
		return "", nil
	}
}
