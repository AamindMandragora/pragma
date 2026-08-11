package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
)

var ansiEscape = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

func firstArg(argsJSON string) string {
	var values map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &values); err != nil {
		return ""
	}
	for _, value := range values {
		return truncateCells(fmt.Sprint(value), 80)
	}
	return ""
}

func windowLines(lines []string, height, offset int) []string {
	if height <= 0 || len(lines) == 0 {
		return nil
	}
	maxOffset := max(0, len(lines)-height)
	offset = max(0, min(offset, maxOffset))
	end := min(len(lines), offset+height)
	return lines[offset:end]
}

func wrapPhysicalLines(lines []string, width int) []string {
	var wrapped []string
	for _, line := range lines {
		if line == "" {
			wrapped = append(wrapped, "")
			continue
		}
		wrapped = append(wrapped, strings.Split(lipgloss.Wrap(line, max(1, width), " "), "\n")...)
	}
	return wrapped
}

func capOutput(value string) string {
	value = sanitizeTerminal(value)
	const limit = 8000
	if len([]rune(value)) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[len(runes)-limit:]) + "\n… output capped; showing tail"
}

func sanitizeTerminal(value string) string {
	return ansiEscape.ReplaceAllString(value, "")
}

func truncateCells(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(value) <= width {
		return value
	}
	var b strings.Builder
	used := 0
	for _, r := range value {
		part := string(r)
		partWidth := lipgloss.Width(part)
		if used+partWidth > max(0, width-1) {
			break
		}
		b.WriteRune(r)
		used += partWidth
	}
	return b.String() + "…"
}

func abbreviatePath(path string) string {
	if path == "" {
		return "."
	}
	if home, err := os.UserHomeDir(); err == nil {
		if relative, err := filepath.Rel(home, path); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return "~/" + relative
		}
	}
	return path
}

func fallback(value, otherwise string) string {
	if value == "" {
		return otherwise
	}
	return value
}
