package db_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AamindMandragora/pragma/internal/db"

	_ "modernc.org/sqlite"
)

// points the package-level connection at a throwaway database and runs the real
// migrations against it, so these tests exercise the shipped schema
func openTestDB(t *testing.T) {
	t.Helper()
	if err := db.ConnectAt(filepath.Join(t.TempDir(), "t.db")); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
}

func sym(id, path, name, receiver, kind string, start, end int) db.Symbol {
	visibility := "unexported"
	if name != "" && strings.ToUpper(name[:1]) == name[:1] {
		visibility = "exported"
	}
	return db.Symbol{
		ID: id, FilePath: path, Name: name, Receiver: receiver, Kind: kind,
		Visibility: visibility, ByteStart: start, ByteEnd: end, BodyHash: "h-" + id,
	}
}

// a path that was never indexed is not an error condition, it is the normal first
// run. treating sql.ErrNoRows as a failure here would mean indexing never starts
func TestFileNeedsReindexUnknownFile(t *testing.T) {
	openTestDB(t)

	need, err := db.FileNeedsReindex("never/seen.go", "abc")
	if err != nil {
		t.Fatalf("unindexed file reported an error: %v", err)
	}
	if !need {
		t.Error("unindexed file should need reindexing")
	}

	if err := db.SetFileIndexed("never/seen.go", "abc"); err != nil {
		t.Fatal(err)
	}
	if need, err = db.FileNeedsReindex("never/seen.go", "abc"); err != nil || need {
		t.Errorf("matching hash should not need reindex (need=%v err=%v)", need, err)
	}
	if need, err = db.FileNeedsReindex("never/seen.go", "different"); err != nil || !need {
		t.Errorf("changed hash should need reindex (need=%v err=%v)", need, err)
	}
	// second call must upsert rather than fail on the primary key
	if err := db.SetFileIndexed("never/seen.go", "different"); err != nil {
		t.Fatalf("re-indexing the same path failed: %v", err)
	}
}

// replacement has to be wholesale: a symbol that disappears from a file must
// disappear from the graph, otherwise renames accumulate forever
func TestReplaceFileSymbolsLeavesNoStrays(t *testing.T) {
	openTestDB(t)
	const path = "internal/tools/registry.go"

	err := db.ReplaceFileSymbols(path, []db.Symbol{
		sym("a1", path, "Registry.List", "Registry", "method", 0, 10),
		sym("a2", path, "Registry.Dispatch", "Registry", "method", 10, 20),
	})
	if err != nil {
		t.Fatal(err)
	}

	// second pass drops Dispatch and adds Invoke
	err = db.ReplaceFileSymbols(path, []db.Symbol{
		sym("a1", path, "Registry.List", "Registry", "method", 0, 10),
		sym("a3", path, "Registry.Invoke", "Registry", "method", 10, 25),
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := db.SymbolByID("a2"); err != db.ErrSymbolNotFound {
		t.Errorf("dropped symbol survived reindex: err = %v, want ErrSymbolNotFound", err)
	}
	if _, err := db.SymbolByID("a3"); err != nil {
		t.Errorf("new symbol missing after reindex: %v", err)
	}
	// the fts triggers must have followed the replacement
	hits, err := db.SearchSymbols("Dispatch", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("search index still returns the dropped symbol: %d hits", len(hits))
	}
}

// the backward walk is the whole point of the graph and of idx_edges_to
func TestCallersAndCallees(t *testing.T) {
	openTestDB(t)
	const path = "internal/agent/agent.go"

	err := db.ReplaceFileSymbols(path, []db.Symbol{
		sym("caller", path, "Agent.Run", "Agent", "method", 0, 10),
		sym("callee", path, "Agent.emit", "Agent", "method", 10, 20),
	})
	if err != nil {
		t.Fatal(err)
	}
	// duplicated deliberately: calling the same function twice is normal and must
	// not trip the (from, to, kind) primary key
	err = db.ReplaceEdges([]db.Edge{
		{From: "caller", To: "callee", Kind: "calls"},
		{From: "caller", To: "callee", Kind: "calls"},
	})
	if err != nil {
		t.Fatalf("duplicate edges should be ignored, not fail: %v", err)
	}

	callers, err := db.Callers("callee")
	if err != nil {
		t.Fatal(err)
	}
	if len(callers) != 1 || callers[0].Name != "Agent.Run" {
		t.Fatalf("Callers(callee) = %v, want [Agent.Run]", callers)
	}

	callees, err := db.Callees("caller")
	if err != nil {
		t.Fatal(err)
	}
	if len(callees) != 1 || callees[0].Name != "Agent.emit" {
		t.Fatalf("Callees(caller) = %v, want [Agent.emit]", callees)
	}

	if got, _ := db.Callers("caller"); len(got) != 0 {
		t.Errorf("Callers(caller) = %v, want none", got)
	}
}

// fts5 reads punctuation as query syntax, so unescaped input raises a sql error
// rather than returning nothing. the caller expects an empty list, never a failure
func TestSearchSymbolsHandlesPunctuation(t *testing.T) {
	openTestDB(t)
	const path = "internal/tools/registry.go"

	err := db.ReplaceFileSymbols(path, []db.Symbol{
		sym("s1", path, "Registry.List", "Registry", "method", 0, 10),
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, q := range []string{"foo-bar", "nothing:here", "trailing*", `quo"te`, "^caret"} {
		if _, err := db.SearchSymbols(q, 10); err != nil {
			t.Errorf("SearchSymbols(%q) returned an error instead of no results: %v", q, err)
		}
	}

	// a qualified name is the natural thing to search for and must still match
	hits, err := db.SearchSymbols("Registry.List", 10)
	if err != nil {
		t.Fatalf("qualified name search failed: %v", err)
	}
	if len(hits) != 1 {
		t.Errorf("SearchSymbols(\"Registry.List\") = %d hits, want 1", len(hits))
	}
	// and the bare method name should find it too
	if hits, err = db.SearchSymbols("List", 10); err != nil || len(hits) != 1 {
		t.Errorf("SearchSymbols(\"List\") = %d hits, err %v; want 1 hit", len(hits), err)
	}
}

// offsets can outlive the bytes they point at. slicing past the end panics, and a
// panic inside a tool takes the whole agent down
func TestSymbolSourceRejectsStaleOffsets(t *testing.T) {
	openTestDB(t)

	dir := t.TempDir()
	file := filepath.Join(dir, "small.go")
	if err := os.WriteFile(file, []byte("package p\n"), fs.FileMode(0644)); err != nil {
		t.Fatal(err)
	}

	good := sym("g", file, "p", "", "type", 0, 7)
	src, err := db.SymbolSource(good)
	if err != nil {
		t.Fatalf("valid range failed: %v", err)
	}
	if src != "package" {
		t.Errorf("SymbolSource = %q, want %q", src, "package")
	}

	for _, bad := range []db.Symbol{
		sym("b1", file, "Gone", "", "func", 0, 9999),
		sym("b2", file, "Gone", "", "func", -1, 5),
		sym("b3", file, "Gone", "", "func", 8, 2),
	} {
		if _, err := db.SymbolSource(bad); err == nil {
			t.Errorf("bytes %d-%d against a %d byte file should error", bad.ByteStart, bad.ByteEnd, 10)
		}
	}
}

// a file removed from disk must not linger in the graph
func TestForgetFile(t *testing.T) {
	openTestDB(t)
	const path = "internal/gone.go"

	if err := db.ReplaceFileSymbols(path, []db.Symbol{sym("z1", path, "Gone", "", "func", 0, 5)}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetFileIndexed(path, "hash"); err != nil {
		t.Fatal(err)
	}
	files, err := db.IndexedFiles()
	if err != nil || len(files) != 1 {
		t.Fatalf("IndexedFiles = %v, err %v; want one entry", files, err)
	}

	if err := db.ForgetFile(path); err != nil {
		t.Fatal(err)
	}
	if files, err = db.IndexedFiles(); err != nil || len(files) != 0 {
		t.Errorf("IndexedFiles after forget = %v, err %v; want empty", files, err)
	}
	if _, err := db.SymbolByID("z1"); err != db.ErrSymbolNotFound {
		t.Errorf("symbol survived ForgetFile: %v", err)
	}
}
