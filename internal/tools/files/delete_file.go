package files

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/AamindMandragora/pragma/internal/process"
)

type DeleteFileTool struct{}

func (d *DeleteFileTool) Name() string {
	return "delete_file"
}

func (d *DeleteFileTool) Description() string {
	return "Deletes a file at the given path"
}

func (d *DeleteFileTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {"path": {"type": "string", "description": "Path to the file to delete"}}, "required": ["path"]}`)
}

func (d *DeleteFileTool) ConfirmSummary(args json.RawMessage) string {
	var params struct {
		Path string `json:"path"`
	}
	json.Unmarshal(args, &params)
	return fmt.Sprintf("delete file %s", params.Path)
}

func (d *DeleteFileTool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	if process.IsIgnored(params.Path) {
		return "", fmt.Errorf("access denied: %s is in .agentignore", params.Path)
	}
	err := os.Remove(params.Path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("deleted file %s", params.Path), nil
}
