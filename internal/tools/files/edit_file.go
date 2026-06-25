package files

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/AamindMandragora/pragma/internal/process"
)

type EditFileTool struct{}

func (e *EditFileTool) Name() string {
	return "edit_file"
}

func (e *EditFileTool) Description() string {
	return "Edits a file by replacing old_text with new_text. The old_text must exactly match some text in the file for the edit to work, including whitespace and indentation."
}

func (e *EditFileTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type": "object","properties": {"path": {"type": "string", "description": "Path to the file to edit"}, "old_text": {"type": "string", "description": "Exact text to find and replace. Must match the file content exactly including whitespace and indentation."}, "new_text": {"type": "string", "description": "Text to replace old_text with. Can be empty to delete the matched text."}},"required": ["path", "old_text", "new_text"]}`)
}

func (e *EditFileTool) ConfirmSummary(args json.RawMessage) string {
	var params struct {
		Path    string `json:"path"`
		OldText string `json:"old_text"`
		NewText string `json:"new_text"`
	}
	json.Unmarshal(args, &params)
	old := params.OldText
	if len(old) > 150 {
		old = old[:150] + "..."
	}
	new := params.NewText
	if len(new) > 150 {
		new = new[:150] + "..."
	}
	return fmt.Sprintf("%s\n  - %s\n  + %s", params.Path, old, new)
}

func (e EditFileTool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		Path    string `json:"path"`
		OldText string `json:"old_text"`
		NewText string `json:"new_text"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	if process.IsIgnored(params.Path) {
		return "", fmt.Errorf("access denied: %s is in .agentignore", params.Path)
	}
	content, err := os.ReadFile(params.Path)
	if err != nil {
		return "", err
	}
	fileContent := string(content)

	count := strings.Count(fileContent, params.OldText)
	if count == 1 {
		newContent := strings.Replace(fileContent, params.OldText, params.NewText, 1)
		if err := os.WriteFile(params.Path, []byte(newContent), 0644); err != nil {
			return "", err
		}
		return fmt.Sprintf("edited %s: replaced %d bytes with %d bytes", params.Path, len(params.OldText), len(params.NewText)), nil
	}
	if count > 1 {
		return fmt.Sprintf("old_text found %d times. Include more surrounding context to make it unique.", count), nil
	}

	oldLines := strings.Split(params.OldText, "\n")
	fileLines := strings.Split(fileContent, "\n")
	matchStart := -1
	for i := 0; i <= len(fileLines)-len(oldLines); i++ {
		match := true
		for j := 0; j < len(oldLines); j++ {
			if strings.TrimSpace(fileLines[i+j]) != strings.TrimSpace(oldLines[j]) {
				match = false
				break
			}
		}
		if match {
			if matchStart != -1 {
				return "old_text matched multiple locations even with fuzzy matching. Include more context.", nil
			}
			matchStart = i
		}
	}
	if matchStart == -1 {
		return "old_text not found in file. Use read_file to check the exact content, then retry with the correct text.", nil
	}

	newLines := strings.Split(params.NewText, "\n")
	result := make([]string, 0, len(fileLines)-len(oldLines)+len(newLines))
	result = append(result, fileLines[:matchStart]...)
	result = append(result, newLines...)
	result = append(result, fileLines[matchStart+len(oldLines):]...)
	newContent := strings.Join(result, "\n")

	if err := os.WriteFile(params.Path, []byte(newContent), 0644); err != nil {
		return "", err
	}
	return fmt.Sprintf("edited %s (fuzzy match): replaced %d lines with %d lines", params.Path, len(oldLines), len(newLines)), nil
}
