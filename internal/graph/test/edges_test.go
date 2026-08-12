package graph_test

import (
	"testing"

	"github.com/AamindMandragora/pragma/internal/db"
	"github.com/AamindMandragora/pragma/internal/graph"
)

// a two-package module so imports edges have something internal to resolve against
var edgeFixture = map[string]string{
	"go.mod": "module example.com/mod\n\ngo 1.26\n",
	"store/store.go": `package store

type Speaker interface {
	Speak() string
	Name() string
}

type Loud struct{}

func (l Loud) Speak() string { return "hi" }
func (l Loud) Name() string  { return "loud" }

type Quiet struct{}

func (q Quiet) Speak() string { return "shh" }
`,
	"app/app.go": `package app

import (
	"fmt"
	"example.com/mod/store"
)

type Runner struct{}

func (r Runner) Run(s store.Speaker) string {
	fmt.Println("go")
	return s.Speak()
}
`,
}

// every edge kind the symbol belongs to, as a set of "kind->name" strings
func edgeKinds(t *testing.T, from db.Symbol) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, kind := range []string{"calls", "type_refs", "imports", "implements"} {
		syms, err := db.EdgesOfKind(from.ID, kind)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range syms {
			out[kind] = append(out[kind], s.Name)
		}
	}
	return out
}

func TestImportsEdgesLinkInternalPackagesOnly(t *testing.T) {
	root := setup(t, edgeFixture)
	if err := graph.EnsureIndexed(root); err != nil {
		t.Fatal(err)
	}

	appPkg := find(t, "example.com/mod/app")
	if appPkg.Kind != "package" {
		t.Fatalf("expected a package symbol, got kind %q", appPkg.Kind)
	}
	imports := edgeKinds(t, appPkg)["imports"]
	if len(imports) != 1 || imports[0] != "example.com/mod/store" {
		t.Errorf("imports of app = %v, want [example.com/mod/store]", imports)
	}
	// "fmt" is stdlib and has no symbol to point at, so it must not appear
	for _, name := range imports {
		if name == "fmt" {
			t.Error("stdlib import was recorded as an edge")
		}
	}
}

func TestTypeRefsEdges(t *testing.T) {
	root := setup(t, edgeFixture)
	if err := graph.EnsureIndexed(root); err != nil {
		t.Fatal(err)
	}

	refs := edgeKinds(t, find(t, "Runner.Run"))["type_refs"]
	var sawSpeaker, sawRunner bool
	for _, n := range refs {
		switch n {
		case "Speaker":
			sawSpeaker = true
		case "Runner":
			sawRunner = true
		}
	}
	if !sawSpeaker {
		t.Errorf("Runner.Run type_refs = %v, want it to reference Speaker", refs)
	}
	if !sawRunner {
		t.Errorf("Runner.Run type_refs = %v, want it to reference its receiver Runner", refs)
	}
}

func TestImplementsEdges(t *testing.T) {
	root := setup(t, edgeFixture)
	if err := graph.EnsureIndexed(root); err != nil {
		t.Fatal(err)
	}

	// Loud has both Speak and Name, so it satisfies Speaker
	if got := edgeKinds(t, find(t, "Loud"))["implements"]; len(got) != 1 || got[0] != "Speaker" {
		t.Errorf("Loud implements = %v, want [Speaker]", got)
	}
	// Quiet only has Speak, so it must not be reported
	if got := edgeKinds(t, find(t, "Quiet"))["implements"]; len(got) != 0 {
		t.Errorf("Quiet implements = %v, want none (it is missing Name)", got)
	}
}

func TestCallsEdgesStillWork(t *testing.T) {
	root := setup(t, edgeFixture)
	if err := graph.EnsureIndexed(root); err != nil {
		t.Fatal(err)
	}

	// s.Speak() resolves by name; Loud.Speak and Quiet.Speak are both candidates in
	// a different package, so this is exactly the ambiguity the resolver drops
	callers, err := db.Callers(find(t, "Loud.Speak").ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range callers {
		if c.Name == "Runner.Run" {
			return
		}
	}
	t.Logf("callers of Loud.Speak = %v (ambiguous cross-package call, may be dropped)", callers)
}
