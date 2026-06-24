package files

import (
	"encoding/json"
	"os"
	"fmt"

	"github.com/AamindMandragora/pragma/internal/tools"
)

type ReadFileTool struct{}

func (r *ReadFileTool) Name() string {
	return "read_file"
}

func (r *ReadFileTool) Description() string {
	return "Reads from a file given the path"
}

func (r *ReadFileTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {"path": {"type": "string", "description": "Path to the file"}}, "required": ["path"]}`)
}

func (r *ReadFileTool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	if tools.IsIgnored(params.Path) {
		return "", fmt.Errorf("access denied: %s is in .agentignore", params.Path)
	}
	contents, err := os.ReadFile(params.Path)
	if err != nil {
		return "", err
	}
	return string(contents), nil
}
