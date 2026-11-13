package cairn

import (
	"database/sql"
)

// Schema changes. Every step can run again safely: each checks what is
// already there, and a step with more than one statement runs them in one
// transaction, so a power cut on a Raspberry Pi leaves either the old schema
// or the finished change, never a column without its values.

// addColumn adds a column and fills it in one transaction. It does nothing
// when the column already exists.
func addColumn(db *sql.DB, table, column, definition, backfill string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var present int
	if err := tx.QueryRow(`SELECT count(*) FROM pragma_table_info(?) WHERE name=?`, table, column).Scan(&present); err != nil {
		return err
	}
	if present > 0 {
		return nil
	}
	if _, err := tx.Exec("ALTER TABLE " + table + " ADD COLUMN " + column + " " + definition); err != nil {
		return err
	}
	if backfill != "" {
		if _, err := tx.Exec(backfill); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// migratePages adds the page columns older versions lacked, oldest first.
func migratePages(db *sql.DB) error {
	for _, column := range []struct{ name, definition string }{
		{"target_url", "TEXT"},
		{"clicks", "INTEGER NOT NULL DEFAULT 0"},
		{"request_summary", "TEXT NOT NULL DEFAULT ''"},
		{"request_source", "TEXT NOT NULL DEFAULT ''"},
		{"preview", "TEXT NOT NULL DEFAULT ''"},
		{"share_revision", "INTEGER NOT NULL DEFAULT 0"},
		// password_checked holds passwordStamp(password_hash) once Cairn
		// knows the password met the 8-character rule. Passwords set by
		// older versions stay unchecked.
		{"password_checked", "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := addColumn(db, "pages", column.name, column.definition, ""); err != nil {
			return err
		}
	}
	return nil
}
