package db

import (
	"database/sql"
	"io/fs"
	"path/filepath"
	"testing"
)

func TestGraphSchema(t *testing.T) {
	conn, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "t.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	for _, f := range []string{"001_initial.sql", "002_session_updates.sql", "003_graph.sql"} {
		b, err := fs.ReadFile(migrations, "migrations/"+f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(string(b)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}

	// confirm the dsn actually turned FK enforcement on
	var fk int
	conn.QueryRow("PRAGMA foreign_keys").Scan(&fk)
	if fk != 1 {
		t.Fatalf("foreign_keys = %d, want 1 (cascades would be inert)", fk)
	}

	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := conn.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := conn.QueryRow(q, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}

	mustExec(`INSERT INTO sessions (id,title,cwd,created_at,updated_at) VALUES ('s1','t','/',1,1)`)
	mustExec(`INSERT INTO symbols VALUES ('a','internal/tools/tools.go','Registry.List','Registry','method','exported',1,2,'h1')`)
	mustExec(`INSERT INTO symbols VALUES ('b','internal/tools/tools.go','Registry.Dispatch','Registry','method','exported',3,4,'h2')`)
	mustExec(`INSERT INTO edges VALUES ('a','b','calls')`)
	mustExec(`INSERT INTO symbol_history VALUES ('b','abc','aashima',100,'modified')`)
	mustExec(`INSERT INTO agent_interactions (session_id,symbol_id,interaction_type,task_context,created_at) VALUES ('s1','b','read','fix tools',1)`)

	// 1. insert trigger populated the search index
	if n := count(`SELECT count(*) FROM symbols_fts WHERE symbols_fts MATCH 'Dispatch'`); n != 1 {
		t.Errorf("fts after insert: got %d hits for 'Dispatch', want 1", n)
	}
	// 2. UNINDEXED file_path must not be searchable, or path words drown real matches
	if n := count(`SELECT count(*) FROM symbols_fts WHERE symbols_fts MATCH 'internal'`); n != 0 {
		t.Errorf("fts matched on file_path: got %d hits for 'internal', want 0", n)
	}
	// 3. update trigger re-indexes under the new name
	mustExec(`UPDATE symbols SET name='Registry.Invoke' WHERE id='b'`)
	if n := count(`SELECT count(*) FROM symbols_fts WHERE symbols_fts MATCH 'Invoke'`); n != 1 {
		t.Errorf("fts after update: got %d hits for new name, want 1", n)
	}
	if n := count(`SELECT count(*) FROM symbols_fts WHERE symbols_fts MATCH 'Dispatch'`); n != 0 {
		t.Errorf("fts after update: stale name still indexed (%d hits)", n)
	}

	// 4. deleting a symbol cascades to edges and git history, but NOT to the interaction log
	mustExec(`DELETE FROM symbols WHERE id='b'`)
	if n := count(`SELECT count(*) FROM edges`); n != 0 {
		t.Errorf("edges did not cascade: %d rows remain", n)
	}
	if n := count(`SELECT count(*) FROM symbol_history`); n != 0 {
		t.Errorf("symbol_history did not cascade: %d rows remain", n)
	}
	if n := count(`SELECT count(*) FROM agent_interactions`); n != 1 {
		t.Errorf("agent_interactions should survive symbol deletion: %d rows, want 1", n)
	}
	// 5. delete trigger cleaned the search index
	if n := count(`SELECT count(*) FROM symbols_fts WHERE symbols_fts MATCH 'Invoke'`); n != 0 {
		t.Errorf("fts after delete: deleted symbol still searchable (%d hits)", n)
	}
}
