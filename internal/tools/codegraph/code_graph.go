// Package codegraph exposes the code graph to the model as a single read-only tool.
package codegraph

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AamindMandragora/pragma/internal/db"
	"github.com/AamindMandragora/pragma/internal/graph"
	"github.com/AamindMandragora/pragma/internal/tools"
)

type Tool struct{}

func (t *Tool) Name() string {
	return "code_graph"
}

// without this the registry treats the tool as AccessExecute and confirms every
// call, which would put a prompt in front of a plain lookup
func (t *Tool) Access() tools.AccessLevel {
	return tools.AccessRead
}

func (t *Tool) Description() string {
	return "Queries the indexed code graph of this repository's Go source. 'search' finds symbols by name and 'source' returns a symbol's text. The relationship modes are 'callers' (what calls this — use it to judge the blast radius of a change), 'callees', 'implementers' (what types satisfy this interface), 'implements', 'importers' (what packages depend on this one), 'imports', and 'references' (what mentions this type). Prefer this over grepping for structural questions."
}

func (t *Tool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"mode":{"type":"string","enum":["search","source","callers","callees","implementers","implements","importers","imports","references"],"description":"Which query to run."},"query":{"type":"string","description":"Search term for mode 'search'; matches symbol names."},"symbol":{"type":"string","description":"Symbol name or id for every mode except 'search'. A name is resolved automatically; if it is ambiguous the candidates are listed. For 'importers' and 'imports' this is a package import path."},"limit":{"type":"integer","minimum":1,"description":"Maximum results for mode 'search'. Defaults to 20."}},"required":["mode"]}`)
}

// each relationship mode is one edge kind walked in one direction. incoming means
// "what points at this", which is the impact-analysis question
var relationModes = map[string]struct {
	kind     string
	incoming bool
}{
	"callers":      {"calls", true},
	"callees":      {"calls", false},
	"implementers": {"implements", true},
	"implements":   {"implements", false},
	"importers":    {"imports", true},
	"imports":      {"imports", false},
	"references":   {"type_refs", true},
}

func (t *Tool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		Mode   string `json:"mode"`
		Query  string `json:"query"`
		Symbol string `json:"symbol"`
		Limit  int    `json:"limit,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}

	// the index is built lazily so sessions that never ask a structural question
	// pay nothing; once built, an unchanged repo costs one hash per file
	if err := graph.EnsureIndexed("."); err != nil {
		return "", fmt.Errorf("indexing failed: %w", err)
	}

	switch params.Mode {
	case "search":
		if strings.TrimSpace(params.Query) == "" {
			return "", fmt.Errorf("query is required for mode 'search'")
		}
		syms, err := db.SearchSymbols(params.Query, params.Limit)
		if err != nil {
			return "", err
		}
		if len(syms) == 0 {
			return fmt.Sprintf("No symbols matching %q.", params.Query), nil
		}
		return formatSymbols(syms), nil

	case "source":
		sym, err := resolveSymbol(params.Symbol)
		if err != nil {
			return "", err
		}
		// package symbols are synthetic and stand for a directory, so there is no
		// source text to slice out of a file
		if sym.Kind == "package" {
			return fmt.Sprintf("%s is a package (%s), not a symbol with source. Use mode 'imports' or 'importers' on it.", sym.Name, sym.FilePath), nil
		}
		src, err := db.SymbolSource(sym)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s %s (%s):\n%s", sym.Kind, sym.Name, sym.FilePath, src), nil
	}

	if rel, ok := relationModes[params.Mode]; ok {
		sym, err := resolveSymbol(params.Symbol)
		if err != nil {
			return "", err
		}
		var syms []db.Symbol
		if rel.incoming {
			syms, err = db.IncomingOfKind(sym.ID, rel.kind)
		} else {
			syms, err = db.EdgesOfKind(sym.ID, rel.kind)
		}
		if err != nil {
			return "", err
		}
		if len(syms) == 0 {
			return fmt.Sprintf("No %s found for %s (%s).", params.Mode, sym.Name, sym.FilePath), nil
		}
		return fmt.Sprintf("%s of %s (%s):\n%s", params.Mode, sym.Name, sym.FilePath, formatSymbols(syms)), nil
	}
	return "", fmt.Errorf("unknown mode %q: use search, source, callers, callees, implementers, implements, importers, imports, or references", params.Mode)
}

// accepts either a symbol id or a name. the model works in names, so an exact id
// is tried first and anything else falls through to a search; an ambiguous name
// comes back as the candidate list rather than an arbitrary pick
func resolveSymbol(nameOrID string) (db.Symbol, error) {
	nameOrID = strings.TrimSpace(nameOrID)
	if nameOrID == "" {
		return db.Symbol{}, fmt.Errorf("symbol is required for this mode")
	}
	if sym, err := db.SymbolByID(nameOrID); err == nil {
		return sym, nil
	}
	syms, err := db.SearchSymbols(nameOrID, 10)
	if err != nil {
		return db.Symbol{}, err
	}
	switch len(syms) {
	case 0:
		return db.Symbol{}, fmt.Errorf("no symbol named %q in the index", nameOrID)
	case 1:
		return syms[0], nil
	}
	// an exact name match among the candidates settles it without asking
	for _, s := range syms {
		if s.Name == nameOrID {
			return s, nil
		}
	}
	return db.Symbol{}, fmt.Errorf("%q is ambiguous, retry with one of these ids:\n%s", nameOrID, formatSymbols(syms))
}

func formatSymbols(syms []db.Symbol) string {
	var b strings.Builder
	for _, s := range syms {
		fmt.Fprintf(&b, "%s %s\t%s\tid=%s\n", s.Kind, s.Name, s.FilePath, s.ID)
	}
	return strings.TrimRight(b.String(), "\n")
}
