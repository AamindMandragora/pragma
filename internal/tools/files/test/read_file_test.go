package files_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/AamindMandragora/pragma/internal/tools/files"
)

func TestReadFileLineRange(t *testing.T) {
	path := t.TempDir() + "/lines.txt"
	contents := "one\ntwo\nthree\nfour\n"
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	tool := &files.ReadFileTool{}

	full, err := tool.Execute(json.RawMessage(`{"path":"` + path + `"}`))
	if err != nil {
		t.Fatal(err)
	}
	if full != contents {
		t.Fatalf("full read changed file contents: %q", full)
	}

	rangeResult, err := tool.Execute(json.RawMessage(`{"path":"` + path + `","start_line":2,"end_line":3}`))
	if err != nil {
		t.Fatal(err)
	}
	if rangeResult != "two\nthree\n" {
		t.Fatalf("unexpected ranged read: %q", rangeResult)
	}

	fromLine, err := tool.Execute(json.RawMessage(`{"path":"` + path + `","start_line":3}`))
	if err != nil {
		t.Fatal(err)
	}
	if fromLine != "three\nfour\n" {
		t.Fatalf("unexpected open-ended read: %q", fromLine)
	}
}

func TestReadFileLineRangeValidation(t *testing.T) {
	path := t.TempDir() + "/lines.txt"
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := (&files.ReadFileTool{}).Execute(json.RawMessage(`{"path":"` + path + `","start_line":3}`))
	if err == nil {
		t.Fatal("expected out-of-range error")
	}
}
