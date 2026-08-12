package graph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
	// import paths exactly as written in the source, used for imports edges
	imports []string
	// interface type name to the method names it requires, for implements edges
	interfaces map[string][]string
	// receiver type name to the methods declared on it in this file. a type's
	// methods can be spread across files, so these are merged package-wide later
	methods map[string][]string
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

	pf := &parsedFile{
		path:       path,
		content:    content,
		tree:       tree,
		interfaces: map[string][]string{},
		methods:    map[string][]string{},
	}

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
			recv := receiverName(decl.ChildByFieldName("receiver"), content)
			add(name.Content(content), recv, "method", decl)
			if recv != "" {
				pf.methods[recv] = append(pf.methods[recv], name.Content(content))
			}
		case "type_declaration":
			for _, spec := range namedChildrenOfType(decl, "type_spec") {
				name := spec.ChildByFieldName("name")
				if name == nil {
					continue
				}
				add(name.Content(content), "", "type", spec)
				// an interface's method set is what implements edges are matched against
				if t := spec.ChildByFieldName("type"); t != nil && t.Type() == "interface_type" {
					var required []string
					walk(t, func(n *sitter.Node) {
						if n.Type() != "method_elem" {
							return
						}
						if m := n.ChildByFieldName("name"); m != nil {
							required = append(required, m.Content(content))
						}
					})
					pf.interfaces[name.Content(content)] = required
				}
			}
		case "import_declaration":
			walk(decl, func(n *sitter.Node) {
				if n.Type() != "import_spec" {
					return
				}
				p := n.ChildByFieldName("path")
				if p == nil {
					return
				}
				// the node text keeps its surrounding quotes
				pf.imports = append(pf.imports, strings.Trim(p.Content(content), `"`+"`"))
			})
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

