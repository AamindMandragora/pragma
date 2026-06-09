package db

import (
	"embed"
	"io/fs"
	"os"

	"database/sql"

	_ "modernc.org/sqlite"
)

// holds a pointer to the database
var db *sql.DB

// all sql files in internal/db/migrations/ will be baked into the executable
//go:embed migrations/*.sql
var migrations embed.FS

// connects to the database and sets it up
func Connect() error {
	// attempts to create the .agent/ directory
	var err = os.MkdirAll(".agent", 0755)
	if err != nil { 
		return err
	}
	// opens .agent/pragma.db, creating if necessary
	db, err = sql.Open("sqlite", ".agent/pragma.db")
	if err != nil {
		return err
	}
	// activates Write-Ahead Logging, which enables writing data while reading data instead of locking the whole file
	_, err = db.Exec("PRAGMA journal_mode=WAL")
	return err
}

// makes sure every sql file under internal/db/migrations/ was run so that our database has updated fields
func Migrate() error {
	// makes a table that holds the names of every sql file that was run
	var _, err = db.Exec("CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY)")
	if err != nil {
		return err
	}
	// gets all the files under internal/db/migrations
	filenames, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		return err
	}
	var v string
	// loops through all the files
	for _, entry := range filenames {
		// checks if the file was previously run
		err = db.QueryRow("SELECT version FROM schema_migrations WHERE version = ?", entry.Name()).Scan(&v)
		// if the file wasn't run then we run it and insert into schema_migrations
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

// database getter function
func DB() *sql.DB {
	return db
}