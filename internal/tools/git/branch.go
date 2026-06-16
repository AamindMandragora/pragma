package git

import "encoding/json"

type BranchTool struct{ gitTool }

func NewBranchTool() *BranchTool {
	return &BranchTool{gitTool{name: "git_branch", description: "Lists, creates, or checks out branches", schema: json.RawMessage(`{"type":"object","properties":{"action":{"type":"string"},"name":{"type":"string"}},"required":["action"]}`)}}
}

func (t *BranchTool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		Action string `json:"action"`
		Name   string `json:"name,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	switch params.Action {
	case "list":
		return runGit("branch", "--format=%(refname:short)")
	case "create":
		if err := required("name", params.Name); err != nil {
			return "", err
		}
		return runGit("branch", params.Name)
	case "checkout":
		if err := required("name", params.Name); err != nil {
			return "", err
		}
		return runGit("checkout", params.Name)
	default:
		return "", nil
	}
}
