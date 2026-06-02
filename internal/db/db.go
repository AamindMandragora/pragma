package db

import (
	"embed"
	"io/fs"
	"os"

	"database/sql"

	_ "modernc.org/sqlite"
)

var db *sql.DB

//go:embed migrations/*.sql
var migrations embed.FS

func Connect() error {
	var err = os.MkdirAll(".agent", 0755)
	if err != nil { 
		return err
	}
	db, err = sql.Open("sqlite", ".agent/pragma.db")
	if err != nil {
		return err
	}
	_, err = db.Exec("PRAGMA journal_mode=WAL")
	return err
}

func Migrate() error {
	var _, err = db.Exec("CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY)")
	if err != nil {
		return err
	}
	filenames, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		return err
	}
	var v string
	for _, entry := range filenames {
		err = db.QueryRow("SELECT version FROM schema_migrations WHERE version = ?", entry.Name()).Scan(&v)
		if err == sql.ErrNoRows {
			var bytes, err = fs.ReadFile(migrations, "migrations/" + entry.Name())
			if err != nil {
				return err
			}
			_, err = db.Exec(string(bytes))
			if err != nil {
				return err
			}
			_, err = db.Exec("INSERT INTO schema_migrations (version) VALUES (?)", entry.Name())
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func DB() *sql.DB {
	return db
}