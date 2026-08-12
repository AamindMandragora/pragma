package graph

import (
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/AamindMandragora/pragma/internal/db"
)

// one synthetic symbol per repo package, so imports edges have something real to
// point at. edges is a symbol-to-symbol table with foreign keys on both ends, and
// an imported package is otherwise not a symbol at all.
//
// only packages inside this module get a row. the standard library and third-party
// dependencies would be fabricated symbols with no file behind them, and the useful
// question ("what still depends on internal/db?") is about this repo anyway
// returns the symbols, a lookup by the directory the files actually live in (which
// may be absolute), and a lookup by import path for resolving import statements
func packageSymbols(files []*parsedFile, modulePath, root string) ([]db.Symbol, map[string]db.Symbol, map[string]db.Symbol) {
	byDir := map[string]db.Symbol{}
	byImport := map[string]db.Symbol{}
	for _, pf := range files {
		dir := filepath.Dir(pf.path)
		if _, seen := byDir[dir]; seen {
			continue
		}
		// the import path is built from the position within the repo, not from the
		// absolute location on disk, so it matches what import statements say
		name := modulePath
		if rel, err := filepath.Rel(root, dir); err == nil && rel != "." && rel != "" {
			name = modulePath + "/" + filepath.ToSlash(rel)
		}
		s := db.Symbol{
			ID:         symbolID(dir, name, "package"),
			FilePath:   dir,
			Name:       name,
			Kind:       "package",
			Visibility: "exported",
			BodyHash:   hashBytes([]byte(name)),
		}
		byDir[dir] = s
		byImport[name] = s
	}
	var syms []db.Symbol
	for _, s := range byDir {
		syms = append(syms, s)
	}
	return syms, byDir, byImport
}

// resolves a bare name against every symbol in the repo, optionally restricted to
// one kind. same file wins, then same directory: both are far likelier than a
// same-named symbol somewhere unrelated
type resolver struct {
	byName map[string][]db.Symbol
}

func newResolver(files []*parsedFile) *resolver {
	r := &resolver{byName: map[string][]db.Symbol{}}
	for _, pf := range files {
		for _, s := range pf.symbols {
			base := s.Name
			if s.Receiver != "" {
				base = strings.TrimPrefix(base, s.Receiver+".")
			}
			r.byName[base] = append(r.byName[base], s)
		}
	}
	return r
}

func (r *resolver) resolve(name, fromFile, kind string) (db.Symbol, bool) {
	var candidates []db.Symbol
	for _, c := range r.byName[name] {
		if kind == "" || c.Kind == kind {
			candidates = append(candidates, c)
		}
	}
	if len(candidates) == 0 {
		return db.Symbol{}, false
	}
	if len(candidates) == 1 {
		return candidates[0], true
	}
	for _, c := range candidates {
		if c.FilePath == fromFile {
			return c, true
		}
	}
	dir := filepath.Dir(fromFile)
	var inPkg []db.Symbol
	for _, c := range candidates {
		if filepath.Dir(c.FilePath) == dir {
			inPkg = append(inPkg, c)
		}
	}
	if len(inPkg) == 1 {
		return inPkg[0], true
	}
	return db.Symbol{}, false
}

// builds every edge kind in one pass over the already-parsed trees.
//
// resolution is name-based and deliberately approximate throughout. a call to
// Dispatch() is matched against every symbol whose base name is Dispatch; anything
// ambiguous, and anything belonging to the standard library or a dependency, is
// dropped. a precise graph needs full type resolution, which tree-sitter does not
// do — a name-based graph is already useful for "who calls this", it just
// occasionally guesses wrong between same-named symbols
func collectEdges(files []*parsedFile, pkgByDir, pkgByImport map[string]db.Symbol) []db.Edge {
	res := newResolver(files)
	var edges []db.Edge

	for _, pf := range files {
		root := pf.tree.RootNode()
		for i := 0; i < int(root.NamedChildCount()); i++ {
			decl := root.NamedChild(i)
			if decl.Type() != "function_declaration" && decl.Type() != "method_declaration" {
				continue
			}
			name := decl.ChildByFieldName("name")
			if name == nil {
				continue
			}
			recv := receiverName(decl.ChildByFieldName("receiver"), pf.content)
			full := name.Content(pf.content)
			kind := "func"
			if recv != "" {
				full = recv + "." + full
				kind = "method"
			}
			from := symbolID(pf.path, full, kind)

			// calls, from the body only
			if body := decl.ChildByFieldName("body"); body != nil {
				walk(body, func(n *sitter.Node) {
					if n.Type() != "call_expression" {
						return
					}
					fn := n.ChildByFieldName("function")
					if fn == nil {
						return
					}
					var called string
					switch fn.Type() {
					case "identifier":
						called = fn.Content(pf.content)
					case "selector_expression":
						// covers both pkg.Func() and value.Method(); only the
						// selected name is used, the operand is not resolved
						if field := fn.ChildByFieldName("field"); field != nil {
							called = field.Content(pf.content)
						}
					}
					if called == "" {
						return
					}
					if to, ok := res.resolve(called, pf.path, ""); ok && to.ID != from {
						edges = append(edges, db.Edge{From: from, To: to.ID, Kind: "calls"})
					}
				})
			}

			// type_refs, across the whole declaration so the signature counts too
			walk(decl, func(n *sitter.Node) {
				if n.Type() != "type_identifier" {
					return
				}
				if to, ok := res.resolve(n.Content(pf.content), pf.path, "type"); ok && to.ID != from {
					edges = append(edges, db.Edge{From: from, To: to.ID, Kind: "type_refs"})
				}
			})
		}
	}

	edges = append(edges, importEdges(files, pkgByDir, pkgByImport)...)
	edges = append(edges, implementsEdges(files, res)...)
	return edges
}

// package to package, for imports that stay inside this module. an import path with
// no entry in pkgByImport is stdlib or a dependency and has no symbol to point at
func importEdges(files []*parsedFile, pkgByDir, pkgByImport map[string]db.Symbol) []db.Edge {
	var edges []db.Edge
	for _, pf := range files {
		from, ok := pkgByDir[filepath.Dir(pf.path)]
		if !ok {
			continue
		}
		for _, imp := range pf.imports {
			if to, ok := pkgByImport[imp]; ok && to.ID != from.ID {
				edges = append(edges, db.Edge{From: from.ID, To: to.ID, Kind: "imports"})
			}
		}
	}
	return edges
}

// type to interface, when the type declares every method the interface requires.
//
// method names only — signatures are not compared, so this over-reports a type that
// happens to have matching names with different parameters. the empty interface is
// skipped because it would otherwise match every type in the repo
func implementsEdges(files []*parsedFile, res *resolver) []db.Edge {
	// a type's methods can be spread over several files, so merge per package
	methodsByPkg := map[string]map[string]map[string]bool{}
	for _, pf := range files {
		dir := filepath.Dir(pf.path)
		if methodsByPkg[dir] == nil {
			methodsByPkg[dir] = map[string]map[string]bool{}
		}
		for recv, names := range pf.methods {
			if methodsByPkg[dir][recv] == nil {
				methodsByPkg[dir][recv] = map[string]bool{}
			}
			for _, n := range names {
				methodsByPkg[dir][recv][n] = true
			}
		}
	}

	var edges []db.Edge
	for _, pf := range files {
		for iface, required := range pf.interfaces {
			if len(required) == 0 {
				continue
			}
			ifaceSym, ok := res.resolve(iface, pf.path, "type")
			if !ok {
				continue
			}
			for dir, types := range methodsByPkg {
				for typeName, has := range types {
					complete := true
					for _, need := range required {
						if !has[need] {
							complete = false
							break
						}
					}
					if !complete {
						continue
					}
					typeSym, ok := res.resolve(typeName, filepath.Join(dir, "x.go"), "type")
					if !ok || typeSym.ID == ifaceSym.ID {
						continue
					}
					edges = append(edges, db.Edge{From: typeSym.ID, To: ifaceSym.ID, Kind: "implements"})
				}
			}
		}
	}
	return edges
}
