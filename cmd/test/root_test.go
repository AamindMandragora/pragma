package cmd_test

import (
	"strings"
	"testing"

	"github.com/AamindMandragora/pragma/cmd"
)

func TestHeadlessPromptUsesArguments(t *testing.T) {
	got, err := cmd.HeadlessPrompt([]string{"fix", "the", "bug"}, strings.NewReader("ignored"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "fix the bug" {
		t.Fatalf("got %q", got)
	}
}

func TestHeadlessPromptReadsStdin(t *testing.T) {
	got, err := cmd.HeadlessPrompt(nil, strings.NewReader("  inspect the repo\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "inspect the repo" {
		t.Fatalf("got %q", got)
	}
}
