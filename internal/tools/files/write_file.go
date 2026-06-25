package files

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/AamindMandragora/pragma/internal/process"
)

type WriteFileTool struct{}

func (w *WriteFileTool) Name() string {
	return "write_file"
}

func (w *WriteFileTool) Description() string {
	return "Writes to a file given the path"
}

func (w *WriteFileTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {"path": {"type": "string", "description": "Path to the file"}, "content": {"type": "string", "description": "Content to write to the file"}}, "required": ["path", "content"]}`)
}

func (w *WriteFileTool) ConfirmSummary(args json.RawMessage) string {
	var params struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	json.Unmarshal(args, &params)
	return fmt.Sprintf("write %d bytes to %s", len(params.Content), params.Path)
}

func (w *WriteFileTool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	if process.IsIgnored(params.Path) {
		return "", fmt.Errorf("access denied: %s is in .agentignore", params.Path)
	}
	if err := os.MkdirAll(filepath.Dir(params.Path), 0755); err != nil {
		return "", err
	}
	err := os.WriteFile(params.Path, []byte(params.Content), 0644)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(params.Content), params.Path), nil
}
