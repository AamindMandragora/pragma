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
	return "Queries the indexed code graph of this repository's Go source. Modes: 'search' finds symbols by name, 'callers' lists what calls a symbol (use this to judge the blast radius of a change), 'callees' lists what a symbol calls, and 'source' returns a symbol's source text. Prefer this over grepping for structural questions."
}

func (t *Tool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"mode":{"type":"string","enum":["search","callers","callees","source"],"description":"Which query to run."},"query":{"type":"string","description":"Search term for mode 'search'; matches symbol names."},"symbol":{"type":"string","description":"Symbol name or id for modes 'callers', 'callees', and 'source'. A name is resolved automatically; if it is ambiguous the candidates are listed."},"limit":{"type":"integer","minimum":1,"description":"Maximum results for mode 'search'. Defaults to 20."}},"required":["mode"]}`)
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

	case "callers", "callees":
		sym, err := resolveSymbol(params.Symbol)
		if err != nil {
			return "", err
		}
		var syms []db.Symbol
		if params.Mode == "callers" {
			syms, err = db.Callers(sym.ID)
		} else {
			syms, err = db.Callees(sym.ID)
		}
		if err != nil {
			return "", err
		}
		if len(syms) == 0 {
			return fmt.Sprintf("No %s found for %s (%s).", params.Mode, sym.Name, sym.FilePath), nil
		}
		return fmt.Sprintf("%s of %s (%s):\n%s", params.Mode, sym.Name, sym.FilePath, formatSymbols(syms)), nil

	case "source":
		sym, err := resolveSymbol(params.Symbol)
		if err != nil {
			return "", err
		}
		src, err := db.SymbolSource(sym)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s %s (%s):\n%s", sym.Kind, sym.Name, sym.FilePath, src), nil
	}
	return "", fmt.Errorf("unknown mode %q: use search, callers, callees, or source", params.Mode)
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
