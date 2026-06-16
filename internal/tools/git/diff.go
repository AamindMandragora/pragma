package git

import "encoding/json"

type DiffTool struct{ gitTool }

func NewDiffTool() *DiffTool {
	return &DiffTool{gitTool{name: "git_diff", description: "Returns git diff for the working tree or an optional ref", schema: json.RawMessage(`{"type":"object","properties":{"ref":{"type":"string"}}}`)}}
}

func (t *DiffTool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		Ref string `json:"ref,omitempty"`
	}
	_ = json.Unmarshal(args, &params)
	if params.Ref != "" {
		return truncate(runGit("diff", params.Ref))
	}
	return truncate(runGit("diff"))
}
