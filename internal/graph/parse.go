package graph

import (
	"crypto/sha256"
	"encoding/hex"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/AamindMandragora/pragma/internal/db"
)

// a file after parsing, kept in memory for the length of one index run so the
// second pass can resolve calls without re-reading or re-parsing anything
type parsedFile struct {
	path    string
	content []byte
	fset    *token.FileSet
	ast     *ast.File
	symbols []db.Symbol
}

// hashes bytes to lowercase hex. used for both graph_meta.content_hash (the whole
// file) and symbols.body_hash (one symbol's own source)
func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// builds a symbol's id. deliberately derived from location and name only, never
// from the body: an id that changed whenever the code changed would detach
// symbol_history and agent_interactions from their symbol on every single edit,
// which is precisely what those tables are designed to survive
func symbolID(filePath, receiver, name string) string {
	sum := sha256.Sum256([]byte(filePath + "\x00" + receiver + "\x00" + name))
	return hex.EncodeToString(sum[:])[:16]
}

// exported/unexported from the first rune, matching Go's own visibility rule
func visibilityOf(name string) string {
	for _, r := range name {
		if unicode.IsUpper(r) {
			return "exported"
		}
		return "unexported"
	}
	return "unexported"
}

// pulls the type name out of a method receiver, unwrapping the pointer and any
// generic type parameters: (r *Registry), (r Registry), (r *Cache[K, V]) all give "Registry"/"Cache"
func receiverName(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	expr := recv.List[0].Type
	for {
		switch t := expr.(type) {
		case *ast.StarExpr:
			expr = t.X
		case *ast.IndexExpr:
			expr = t.X
		case *ast.IndexListExpr:
			expr = t.X
		case *ast.Ident:
			return t.Name
		default:
			return ""
		}
	}
}

// parses one Go file into its top-level symbols. byte offsets come straight from
// the token.FileSet, which already counts in bytes, so they can be handed to
// db.SymbolSource to slice the source back out later
func parseFile(path string, content []byte) (*parsedFile, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, content, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	pf := &parsedFile{path: path, content: content, fset: fset, ast: file}

	offset := func(p token.Pos) int { return fset.Position(p).Offset }

	add := func(name, receiver, kind string, start, end int) {
		if name == "" || name == "_" {
			return
		}
		full := name
		if receiver != "" {
			full = receiver + "." + name
		}
		if start < 0 || end > len(content) || end < start {
			return
		}
		pf.symbols = append(pf.symbols, db.Symbol{
			ID:         symbolID(path, receiver, full),
			FilePath:   path,
			Name:       full,
			Receiver:   receiver,
			Kind:       kind,
			Visibility: visibilityOf(name),
			ByteStart:  start,
			ByteEnd:    end,
			BodyHash:   hashBytes(content[start:end]),
		})
	}

	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			recv := receiverName(d.Recv)
			kind := "func"
			if recv != "" {
				kind = "method"
			}
			add(d.Name.Name, recv, kind, offset(d.Pos()), offset(d.End()))
		case *ast.GenDecl:
			// only package-level declarations are symbols; anything inside a
			// function body belongs to the enclosing symbol, not to itself
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					add(s.Name.Name, "", "type", offset(s.Pos()), offset(s.End()))
				case *ast.ValueSpec:
					kind := "var"
					if d.Tok == token.CONST {
						kind = "const"
					}
					for _, ident := range s.Names {
						add(ident.Name, "", kind, offset(s.Pos()), offset(s.End()))
					}
				}
			}
		}
	}
	return pf, nil
}

// resolves calls across every parsed file into edges.
//
// resolution is name-based and deliberately approximate. a call to Dispatch() is
// matched against every symbol whose base name is Dispatch, preferring one in the
// same file and then one in the same package; anything still ambiguous, and
// anything belonging to the standard library or a dependency, is dropped. a
// precise graph needs go/types and full type checking, which is a much larger
// commitment than this buys — a name-based graph is already useful for "who calls
// this", it just occasionally guesses wrong between same-named methods
func collectEdges(files []*parsedFile) []db.Edge {
	// base name (method name without its receiver) to every symbol that answers to it
	byName := map[string][]db.Symbol{}
	for _, pf := range files {
		for _, s := range pf.symbols {
			base := s.Name
			if s.Receiver != "" {
				base = strings.TrimPrefix(base, s.Receiver+".")
			}
			byName[base] = append(byName[base], s)
		}
	}

	resolve := func(name, fromFile string) (db.Symbol, bool) {
		candidates := byName[name]
		if len(candidates) == 0 {
			return db.Symbol{}, false
		}
		if len(candidates) == 1 {
			return candidates[0], true
		}
		// same file wins, then same directory: both are far likelier than a
		// same-named symbol somewhere unrelated in the repo
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

	var edges []db.Edge
	for _, pf := range files {
		for _, decl := range pf.ast.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			recv := receiverName(fn.Recv)
			full := fn.Name.Name
			if recv != "" {
				full = recv + "." + full
			}
			from := symbolID(pf.path, recv, full)

			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				var name string
				switch f := call.Fun.(type) {
				case *ast.Ident:
					name = f.Name
				case *ast.SelectorExpr:
					// covers both pkg.Func() and value.Method(); the receiver
					// expression is not resolved, only the selected name
					name = f.Sel.Name
				}
				if name == "" {
					return true
				}
				if to, ok := resolve(name, pf.path); ok && to.ID != from {
					edges = append(edges, db.Edge{From: from, To: to.ID, Kind: "calls"})
				}
				return true
			})
		}
	}
	return edges
}
