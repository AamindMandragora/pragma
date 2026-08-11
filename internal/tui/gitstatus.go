package tui

import (
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
)

func gitStatusCmd() tea.Cmd {
	return func() tea.Msg {
		cwd := workingDirectory()
		branchOutput, branchErr := exec.Command("git", "branch", "--show-current").Output()
		statusOutput, statusErr := exec.Command("git", "status", "--short").Output()
		if branchErr != nil && statusErr != nil {
			return GitStatusMsg{Path: cwd, Err: statusErr}
		}
		files := parseGitStatus(string(statusOutput))
		return GitStatusMsg{Branch: strings.TrimSpace(string(branchOutput)), Path: cwd, Files: files}
	}
}

func currentChangedFiles() []ChangedFile {
	output, err := exec.Command("git", "status", "--short").Output()
	if err != nil {
		return nil
	}
	return parseGitStatus(string(output))
}

func parseGitStatus(output string) []ChangedFile {
	var files []ChangedFile
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if len(line) < 4 {
			continue
		}
		status := strings.TrimSpace(line[:2])
		path := strings.TrimSpace(line[3:])
		if strings.Contains(path, " -> ") {
			path = strings.TrimSpace(strings.SplitN(path, " -> ", 2)[1])
		}
		files = append(files, ChangedFile{Status: fallback(status, "M"), Path: path})
	}
	return files
}

func workingDirectory() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}
