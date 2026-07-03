package files

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/AamindMandragora/pragma/internal/process"
)

type MoveFileTool struct{}

func (m *MoveFileTool) Name() string { return "move_file" }
func (m *MoveFileTool) Description() string {
	return "Moves or renames a file from one path to another"
}
func (m *MoveFileTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"from":{"type":"string","description":"Source file path"},"to":{"type":"string","description":"Destination file path"}},"required":["from","to"]}`)
}
func (m *MoveFileTool) ConfirmSummary(args json.RawMessage) string {
	var params struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	_ = json.Unmarshal(args, &params)
	return fmt.Sprintf("move %s to %s", params.From, params.To)
}
func (m *MoveFileTool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if process.IsIgnored(params.From) {
		return "", fmt.Errorf("access denied: %s is in .agentignore", params.From)
	}
	if process.IsIgnored(params.To) {
		return "", fmt.Errorf("access denied: %s is in .agentignore", params.To)
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	if params.From == "" || params.To == "" {
		return "", fmt.Errorf("from and to are required")
	}
	if err := os.MkdirAll(filepath.Dir(params.To), 0755); err != nil {
		return "", err
	}
	if err := os.Rename(params.From, params.To); err != nil {
		return "", err
	}
	return fmt.Sprintf("moved %s to %s", params.From, params.To), nil
}
