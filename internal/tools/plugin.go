package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AamindMandragora/pragma/internal/process"
)

type PluginManifest struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
	Command     string          `json:"command"`
	Confirm     bool            `json:"confirm"`
}

type PluginTool struct {
	Manifest PluginManifest
	Manager *process.Manager
}

func LoadPlugins(registry *Registry, path string, manager *process.Manager) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			manifestPath := filepath.Join(path, entry.Name(), "tool.json")
			data, err := os.ReadFile(manifestPath)
			if err != nil {
				continue
			}
			var manifest PluginManifest
			if err := json.Unmarshal(data, &manifest); err != nil {
				continue
			}
			ptool := &PluginTool{Manifest: manifest, Manager: manager}
			registry.Register(ptool)
		}
	}
}

func (p* PluginTool) Name() string {
	return p.Manifest.Name
}

func (p* PluginTool) Description() string {
	return p.Manifest.Description
}

func (p* PluginTool) Schema() json.RawMessage {
	return p.Manifest.Schema
}

func (p* PluginTool) Execute(args json.RawMessage) (string, error) {
	var params map[string]interface{}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	cmd := p.Manifest.Command
	for key, val := range params {
		placeholder := "{{" + key + "}}"
		cmd = strings.ReplaceAll(cmd, placeholder, fmt.Sprintf("%v", val))
	}
	proc, err := p.Manager.Start(cmd, 5 * time.Minute)
	if err != nil {
		return "", err
	}
	result := proc.Wait()
	output := result.Stdout.String()
	stderr := result.Stderr.String()
	if stderr != "" {
		output += "\nstderr:\n" + stderr
	}
	if result.ExitCode != 0 {
		output += fmt.Sprintf("\nexit code: %d", result.ExitCode)
	}
	result.Stdout.Close()
	result.Stderr.Close()
	return output, nil
}

func (p* PluginTool) ConfirmSummary(args json.RawMessage) string {
	if !p.Manifest.Confirm {
		return ""
	}
	var params map[string]interface{}
	json.Unmarshal(args, &params)
	cmd := p.Manifest.Command
	for key, val := range params {
		cmd = strings.ReplaceAll(cmd, "{{" + key + "}}", fmt.Sprintf("%v", val))
	}
	return cmd
}