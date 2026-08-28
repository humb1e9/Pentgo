package project

import (
	"database/sql"

	"pentgo/internal/storage"
)

// OpenSQLite keeps the legacy project entrypoint while SQLite ownership moves
// to the storage layer.
func OpenSQLite(path string) (*sql.DB, error) {
	return storage.OpenSQLite(path)
}
