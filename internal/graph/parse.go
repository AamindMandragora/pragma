package graph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"

	"github.com/AamindMandragora/pragma/internal/db"
)

// returned when a file does not parse cleanly. tree-sitter is error-tolerant and
// still hands back a tree, so this is raised deliberately rather than by the parser
var errSyntax = errors.New("file contains syntax errors")

// a file after parsing, kept in memory for the length of one index run so the
// second pass can resolve calls without re-reading or re-parsing anything.
// the tree holds C memory and has to be closed when the run finishes
type parsedFile struct {
	path    string
	content []byte
	tree    *sitter.Tree
	symbols []db.Symbol
}

func (p *parsedFile) Close() {
	if p.tree != nil {
		p.tree.Close()
	}
}

// hashes bytes to lowercase hex. used for both graph_meta.content_hash (the whole
// file) and symbols.body_hash (one symbol's own source)
func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// builds a symbol's id from file path, name, and kind. deliberately derived from
// location and identity only, never from the body: an id that changed whenever the
// code changed would detach symbol_history and agent_interactions from their symbol
// on every single edit, which is precisely what those tables are designed to survive
func symbolID(filePath, name, kind string) string {
	sum := sha256.Sum256([]byte(filePath + "\x00" + name + "\x00" + kind))
	return hex.EncodeToString(sum[:])[:16]
}

// exported/unexported from the first rune, matching Go's own visibility rule.
// an empty name decodes to RuneError, which is not upper, so it reads as unexported
func visibilityOf(name string) string {
	r, _ := utf8.DecodeRuneInString(name)
	if unicode.IsUpper(r) {
		return "exported"
	}
	return "unexported"
}

// visits every node in the subtree
func walk(n *sitter.Node, fn func(*sitter.Node)) {
	if n == nil {
		return
	}
	fn(n)
	for i := 0; i < int(n.NamedChildCount()); i++ {
		walk(n.NamedChild(i), fn)
	}
}

// first descendant of the given type, or nil. used to dig a bare type name out of
// wrappers like *Registry or Cache[K, V]
func firstOfType(n *sitter.Node, kind string) *sitter.Node {
	var found *sitter.Node
	walk(n, func(c *sitter.Node) {
		if found == nil && c.Type() == kind {
			found = c
		}
	})
	return found
}

// every child registered under a repeated field name. ChildByFieldName only ever
// returns the first, which would silently drop the second name in `var a, b = 1, 2`
func childrenByField(n *sitter.Node, field string) []*sitter.Node {
	var out []*sitter.Node
	for i := 0; i < int(n.ChildCount()); i++ {
		if n.FieldNameForChild(i) == field {
			out = append(out, n.Child(i))
		}
	}
	return out
}

// pulls the type name out of a method receiver: (r *Registry), (r Registry), and
// (c *Cache[K, V]) all give "Registry"/"Cache"
func receiverName(recv *sitter.Node, src []byte) string {
	if recv == nil {
		return ""
	}
	if id := firstOfType(recv, "type_identifier"); id != nil {
		return id.Content(src)
	}
	return ""
}

// parses one Go file into its top-level symbols. tree-sitter reports byte offsets
// directly, which is what the schema stores, so a symbol's source can be sliced
// back out of the file on demand instead of being copied into the database
func parseFile(path string, content []byte) (*parsedFile, error) {
	parser := sitter.NewParser()
	defer parser.Close()
	parser.SetLanguage(golang.GetLanguage())

	tree, err := parser.ParseCtx(context.Background(), nil, content)
	if err != nil {
		return nil, err
	}
	root := tree.RootNode()
	// tree-sitter always produces a tree, marking what it could not understand with
	// ERROR nodes rather than failing. a file caught mid-edit would otherwise be
	// indexed with half its symbols missing, so it is skipped until it parses cleanly
	if root.HasError() {
		tree.Close()
		return nil, errSyntax
	}

	pf := &parsedFile{path: path, content: content, tree: tree}

	add := func(name, receiver, kind string, n *sitter.Node) {
		if name == "" || name == "_" {
			return
		}
		full := name
		if receiver != "" {
			full = receiver + "." + name
		}
		start, end := int(n.StartByte()), int(n.EndByte())
		if start < 0 || end > len(content) || end < start {
			return
		}
		pf.symbols = append(pf.symbols, db.Symbol{
			ID:         symbolID(path, full, kind),
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

	// only top-level declarations are symbols; anything nested inside a function
	// body belongs to the enclosing symbol rather than standing on its own
	for i := 0; i < int(root.NamedChildCount()); i++ {
		decl := root.NamedChild(i)
		switch decl.Type() {
		case "function_declaration":
			if name := decl.ChildByFieldName("name"); name != nil {
				add(name.Content(content), "", "func", decl)
			}
		case "method_declaration":
			name := decl.ChildByFieldName("name")
			if name == nil {
				continue
			}
			add(name.Content(content), receiverName(decl.ChildByFieldName("receiver"), content), "method", decl)
		case "type_declaration":
			for _, spec := range namedChildrenOfType(decl, "type_spec") {
				if name := spec.ChildByFieldName("name"); name != nil {
					add(name.Content(content), "", "type", spec)
				}
			}
		case "const_declaration", "var_declaration":
			kind := "var"
			specType := "var_spec"
			if decl.Type() == "const_declaration" {
				kind, specType = "const", "const_spec"
			}
			for _, spec := range namedChildrenOfType(decl, specType) {
				for _, name := range childrenByField(spec, "name") {
					add(name.Content(content), "", kind, spec)
				}
			}
		}
	}
	return pf, nil
}

// specs of the given type anywhere under a declaration, which covers both the bare
// form (`type Foo struct{}`) and the parenthesised list form
func namedChildrenOfType(decl *sitter.Node, kind string) []*sitter.Node {
	var out []*sitter.Node
	walk(decl, func(n *sitter.Node) {
		if n.Type() == kind {
			out = append(out, n)
		}
	})
	return out
}

// resolves calls across every parsed file into edges.
//
// resolution is name-based and deliberately approximate. a call to Dispatch() is
// matched against every symbol whose base name is Dispatch, preferring one in the
// same file and then one in the same package; anything still ambiguous, and
// anything belonging to the standard library or a dependency, is dropped. a precise
// graph needs full type resolution, which tree-sitter does not do and which is a
// much larger commitment than this buys — a name-based graph is already useful for
// "who calls this", it just occasionally guesses wrong between same-named methods
func collectEdges(files []*parsedFile) []db.Edge {
	// base name (method name without its receiver) to every symbol answering to it
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
		root := pf.tree.RootNode()
		for i := 0; i < int(root.NamedChildCount()); i++ {
			decl := root.NamedChild(i)
			if decl.Type() != "function_declaration" && decl.Type() != "method_declaration" {
				continue
			}
			body := decl.ChildByFieldName("body")
			name := decl.ChildByFieldName("name")
			if body == nil || name == nil {
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
					// covers both pkg.Func() and value.Method(); only the selected
					// name is used, the operand expression is not resolved
					if field := fn.ChildByFieldName("field"); field != nil {
						called = field.Content(pf.content)
					}
				}
				if called == "" {
					return
				}
				if to, ok := resolve(called, pf.path); ok && to.ID != from {
					edges = append(edges, db.Edge{From: from, To: to.ID, Kind: "calls"})
				}
			})
		}
	}
	return edges
}
