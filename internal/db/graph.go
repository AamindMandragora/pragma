package db

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// returned when a lookup by symbol id finds nothing. the graph is rebuilt from
// source, so a stale id is a normal condition rather than a failure
var ErrSymbolNotFound = errors.New("symbol not found")

// struct to store as symbols and file location (in terms of bit)
// so we won't need to parse everytime
type Symbol struct {
	ID         string
	FilePath   string
	Name       string
	Receiver   string
	Kind       string
	Visibility string
	ByteStart  int
	ByteEnd    int
	BodyHash   string
}

// one relationship between two symbols. Kind is calls, imports, type_refs, or implements
type Edge struct {
	From string
	To   string
	Kind string
}

// column lists kept in the order they appear in 003_graph.sql so every scan lines up
const symbolColumns = "id, file_path, name, receiver, kind, visibility, byte_start, byte_end, body_hash"
const symbolColumnsAliased = "s.id, s.file_path, s.name, s.receiver, s.kind, s.visibility, s.byte_start, s.byte_end, s.body_hash"

// reads a symbol result set into a slice. rows.Next() stops on both "done" and
// "broke", so the rows.Err() check afterwards is what tells the two apart
func scanSymbols(rows *sql.Rows) ([]Symbol, error) {
	defer rows.Close()
	var syms []Symbol
	for rows.Next() {
		var s Symbol
		err := rows.Scan(&s.ID, &s.FilePath, &s.Name, &s.Receiver, &s.Kind, &s.Visibility, &s.ByteStart, &s.ByteEnd, &s.BodyHash)
		if err != nil {
			return nil, err
		}
		syms = append(syms, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return syms, nil
}

// reports whether a file has to be parsed again. a missing row means the file was
// never indexed, which is the common case on a first run and is not an error
func FileNeedsReindex(filePath, contentHash string) (bool, error) {
	var stored string
	var err = db.QueryRow("SELECT content_hash FROM graph_meta WHERE file_path = ?", filePath).Scan(&stored)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return stored != contentHash, nil
}

// records that a file was indexed at its current contents. file_path is the primary
// key, so this has to upsert: a plain insert would fail on every reindex
func SetFileIndexed(filePath, contentHash string) error {
	var ctim = time.Now()
	var _, err = db.Exec(`INSERT INTO graph_meta (file_path, content_hash, indexed_at) VALUES (?, ?, ?)
		ON CONFLICT(file_path) DO UPDATE SET content_hash = excluded.content_hash, indexed_at = excluded.indexed_at`,
		filePath, contentHash, ctim.Unix())
	return err
}

// removes every symbol belonging to a file. the cascades and fts triggers in
// 003_graph.sql do the rest: edges, symbol_history, and the search index all follow.
// note this also drops edges pointing *into* these symbols from other files, which
// is why the indexer rebuilds the whole edge table after any symbol change
func DeleteFileSymbols(filePath string) error {
	var _, err = db.Exec("DELETE FROM symbols WHERE file_path = ?", filePath)
	return err
}

// every file path currently recorded in the index. the indexer diffs this against
// what is actually on disk so that deleted files do not linger in the graph
func IndexedFiles() ([]string, error) {
	rows, err := db.Query("SELECT file_path FROM graph_meta")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return paths, nil
}

// drops a file from the index entirely, symbols and bookkeeping alike
func ForgetFile(filePath string) error {
	if err := DeleteFileSymbols(filePath); err != nil {
		return err
	}
	var _, err = db.Exec("DELETE FROM graph_meta WHERE file_path = ?", filePath)
	return err
}

// replaces a file's symbols wholesale. symbols get renamed and deleted, not just
// edited, so a per-row upsert would strand the old ones forever.
//
// this is the first transaction in the project and it needs to be one: a crash
// between the delete and the inserts leaves the file half-indexed while graph_meta
// still reports it as current, and nothing would ever repair that
func ReplaceFileSymbols(filePath string, syms []Symbol) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	// no-op once Commit succeeds, so every early return below is automatically safe
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM symbols WHERE file_path = ?", filePath); err != nil {
		return err
	}
	// prepared once instead of re-parsed for every symbol in the file
	stmt, err := tx.Prepare("INSERT INTO symbols (" + symbolColumns + ") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, s := range syms {
		_, err = stmt.Exec(s.ID, s.FilePath, s.Name, s.Receiver, s.Kind, s.Visibility, s.ByteStart, s.ByteEnd, s.BodyHash)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// rebuilds the entire edge table in one transaction. edges are global: reindexing
// one file can invalidate edges owned by files that did not change, so they are
// always rebuilt together rather than patched.
//
// OR IGNORE is required, not defensive. the primary key is (from, to, kind), and a
// function that calls another one twice legitimately produces two identical edges
func ReplaceEdges(edges []Edge) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM edges"); err != nil {
		return err
	}
	stmt, err := tx.Prepare("INSERT OR IGNORE INTO edges (from_symbol_id, to_symbol_id, kind) VALUES (?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, e := range edges {
		if _, err = stmt.Exec(e.From, e.To, e.Kind); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// keyword search over symbol names, best match first
func SearchSymbols(query string, limit int) ([]Symbol, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	// fts5 reads -, :, *, ^ and " as query syntax, so a bare name like Registry.List
	// or foo-bar raises a sql syntax error rather than returning no rows. quoting the
	// whole term forces it to be read as a literal phrase, and interior quotes double
	var phrase = `"` + strings.ReplaceAll(query, `"`, `""`) + `"`
	// symbols_fts only holds the name plus two UNINDEXED tag-alongs, so the real row
	// has to be joined back. the triggers insert with rowid = new.rowid, which makes
	// sqlite's implicit rowid the join key
	rows, err := db.Query(`SELECT `+symbolColumnsAliased+`
		FROM symbols_fts
		JOIN symbols s ON s.rowid = symbols_fts.rowid
		WHERE symbols_fts MATCH ?
		ORDER BY rank
		LIMIT ?`, phrase, limit)
	if err != nil {
		return nil, err
	}
	return scanSymbols(rows)
}

// every symbol that calls this one. this is the impact-analysis direction and the
// reason idx_edges_to exists: the primary key only covers the forward walk
func Callers(symbolID string) ([]Symbol, error) {
	rows, err := db.Query(`SELECT `+symbolColumnsAliased+`
		FROM edges e
		JOIN symbols s ON s.id = e.from_symbol_id
		WHERE e.to_symbol_id = ? AND e.kind = 'calls'
		ORDER BY s.file_path, s.name`, symbolID)
	if err != nil {
		return nil, err
	}
	return scanSymbols(rows)
}

// every symbol this one calls
func Callees(symbolID string) ([]Symbol, error) {
	rows, err := db.Query(`SELECT `+symbolColumnsAliased+`
		FROM edges e
		JOIN symbols s ON s.id = e.to_symbol_id
		WHERE e.from_symbol_id = ? AND e.kind = 'calls'
		ORDER BY s.file_path, s.name`, symbolID)
	if err != nil {
		return nil, err
	}
	return scanSymbols(rows)
}

// looks up one symbol by id, returning ErrSymbolNotFound when it is gone
func SymbolByID(id string) (Symbol, error) {
	var s Symbol
	var err = db.QueryRow("SELECT "+symbolColumns+" FROM symbols WHERE id = ?", id).
		Scan(&s.ID, &s.FilePath, &s.Name, &s.Receiver, &s.Kind, &s.Visibility, &s.ByteStart, &s.ByteEnd, &s.BodyHash)
	if err == sql.ErrNoRows {
		return Symbol{}, ErrSymbolNotFound
	}
	if err != nil {
		return Symbol{}, err
	}
	return s, nil
}

// slices a symbol's source back out of its file. the graph stores offsets rather
// than the text itself so it cannot go stale against the file, but that means the
// offsets can outlive the bytes they point at: slicing past the end of a file that
// shrank would panic and take the agent down with it, so the range is checked first
func SymbolSource(s Symbol) (string, error) {
	contents, err := os.ReadFile(s.FilePath)
	if err != nil {
		return "", err
	}
	if s.ByteStart < 0 || s.ByteEnd < s.ByteStart || s.ByteEnd > len(contents) {
		return "", fmt.Errorf("stale index for %s in %s: bytes %d-%d but file is %d bytes; reindex needed",
			s.Name, s.FilePath, s.ByteStart, s.ByteEnd, len(contents))
	}
	return string(contents[s.ByteStart:s.ByteEnd]), nil
}
