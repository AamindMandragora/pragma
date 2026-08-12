package graph_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/AamindMandragora/pragma/internal/db"
	"github.com/AamindMandragora/pragma/internal/graph"

	_ "modernc.org/sqlite"
)

const fixture = `package fixture

type Registry struct{}

func helper() int { return 1 }

func (r *Registry) Dispatch() int { return helper() }

const MaxThings = 3
`

// builds a throwaway repo and a throwaway database, and returns the repo root
func setup(t *testing.T, files map[string]string) string {
	t.Helper()
	if err := db.ConnectAt(filepath.Join(t.TempDir(), "t.db")); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// looks up exactly one symbol by its full name
func find(t *testing.T, name string) db.Symbol {
	t.Helper()
	syms, err := db.SearchSymbols(name, 20)
	if err != nil {
		t.Fatalf("SearchSymbols(%q): %v", name, err)
	}
	for _, s := range syms {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("symbol %q not in the index (got %d other hits)", name, len(syms))
	return db.Symbol{}
}

func TestIndexerExtractsSymbols(t *testing.T) {
	root := setup(t, map[string]string{"a.go": fixture})
	if err := graph.EnsureIndexed(root); err != nil {
		t.Fatal(err)
	}

	cases := []struct{ name, kind, receiver, visibility string }{
		{"Registry", "type", "", "exported"},
		{"helper", "func", "", "unexported"},
		{"Registry.Dispatch", "method", "Registry", "exported"},
		{"MaxThings", "const", "", "exported"},
	}
	for _, c := range cases {
		s := find(t, c.name)
		if s.Kind != c.kind || s.Receiver != c.receiver || s.Visibility != c.visibility {
			t.Errorf("%s: kind=%q receiver=%q visibility=%q; want %q/%q/%q",
				c.name, s.Kind, s.Receiver, s.Visibility, c.kind, c.receiver, c.visibility)
		}
	}

	// byte offsets must slice the real source back out
	src, err := db.SymbolSource(find(t, "helper"))
	if err != nil {
		t.Fatalf("SymbolSource: %v", err)
	}
	if src != "func helper() int { return 1 }" {
		t.Errorf("SymbolSource = %q", src)
	}
}

func TestIndexerBuildsCallEdges(t *testing.T) {
	root := setup(t, map[string]string{"a.go": fixture})
	if err := graph.EnsureIndexed(root); err != nil {
		t.Fatal(err)
	}

	callers, err := db.Callers(find(t, "helper").ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(callers) != 1 || callers[0].Name != "Registry.Dispatch" {
		t.Fatalf("Callers(helper) = %v, want [Registry.Dispatch]", callers)
	}

	callees, err := db.Callees(find(t, "Registry.Dispatch").ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(callees) != 1 || callees[0].Name != "helper" {
		t.Fatalf("Callees(Registry.Dispatch) = %v, want [helper]", callees)
	}
}

// ids are derived from location and name only. if a body change moved a symbol's
// id, symbol_history and agent_interactions would detach on every edit
func TestIndexerIDsSurviveBodyChanges(t *testing.T) {
	root := setup(t, map[string]string{"a.go": fixture})
	if err := graph.EnsureIndexed(root); err != nil {
		t.Fatal(err)
	}
	before := find(t, "helper")

	changed := `package fixture

type Registry struct{}

func helper() int { return 42 }

func (r *Registry) Dispatch() int { return helper() }

const MaxThings = 3
`
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte(changed), 0644); err != nil {
		t.Fatal(err)
	}
	if err := graph.EnsureIndexed(root); err != nil {
		t.Fatal(err)
	}
	after := find(t, "helper")

	if after.ID != before.ID {
		t.Errorf("id changed with the body: %s -> %s", before.ID, after.ID)
	}
	if after.BodyHash == before.BodyHash {
		t.Error("body_hash did not change when the body did")
	}
	// the edge must survive the rebuild
	if callers, _ := db.Callers(after.ID); len(callers) != 1 {
		t.Errorf("edges lost after reindex: %d callers, want 1", len(callers))
	}
}

// re-running against an unchanged tree must be a no-op, and must not lose anything
func TestIndexerSecondRunIsStable(t *testing.T) {
	root := setup(t, map[string]string{"a.go": fixture})
	if err := graph.EnsureIndexed(root); err != nil {
		t.Fatal(err)
	}
	first := find(t, "Registry.Dispatch")
	if err := graph.EnsureIndexed(root); err != nil {
		t.Fatal(err)
	}
	second := find(t, "Registry.Dispatch")

	if first != second {
		t.Errorf("symbol changed across identical runs:\n %+v\n %+v", first, second)
	}
	if callers, _ := db.Callers(find(t, "helper").ID); len(callers) != 1 {
		t.Errorf("edge table not rebuilt cleanly: %d callers, want 1", len(callers))
	}
}

func TestIndexerForgetsDeletedFiles(t *testing.T) {
	root := setup(t, map[string]string{
		"a.go":     fixture,
		"b/oth.go": "package b\n\nfunc Orphan() {}\n",
	})
	if err := graph.EnsureIndexed(root); err != nil {
		t.Fatal(err)
	}
	find(t, "Orphan")

	if err := os.Remove(filepath.Join(root, "b/oth.go")); err != nil {
		t.Fatal(err)
	}
	if err := graph.EnsureIndexed(root); err != nil {
		t.Fatal(err)
	}

	hits, err := db.SearchSymbols("Orphan", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("deleted file's symbols survived: %v", hits)
	}
	files, err := db.IndexedFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Errorf("IndexedFiles = %v, want only a.go", files)
	}
}
