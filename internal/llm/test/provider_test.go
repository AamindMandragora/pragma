package llm_test

import (
	"strings"
	"testing"

	"github.com/AamindMandragora/pragma/internal/llm"
)

func TestNewStreamScannerAcceptsLargeSSELines(t *testing.T) {
	line := "data: " + strings.Repeat("x", 128*1024) + "\n"
	scanner := llm.NewStreamScanner(strings.NewReader(line))
	if !scanner.Scan() {
		t.Fatalf("scanner rejected a large SSE line: %v", scanner.Err())
	}
	if len(scanner.Text()) != len(line)-1 {
		t.Fatalf("scanner returned %d bytes, want %d", len(scanner.Text()), len(line)-1)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
}
