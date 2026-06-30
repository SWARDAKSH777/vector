package sqlite3local

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"
)

func TestIsConstraintUsesTypedSQLiteCode(t *testing.T) {
	constraint := &Error{Code: 19, Message: "unique constraint failed"}
	if !IsConstraint(constraint) || !IsConstraint(fmt.Errorf("insert failed: %w", constraint)) {
		t.Fatal("typed or wrapped SQLite constraint was not recognized")
	}
	if IsConstraint(&Error{Code: 10, Message: "disk I/O error"}) || IsConstraint(errors.New("constraint failed")) {
		t.Fatal("non-constraint error was misclassified as a conflict")
	}
}

func TestConstraintCodeFromNoArgumentExec(t *testing.T) {
	db, err := sql.Open("sqlite3", t.TempDir()+"/constraint.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE items(id INTEGER PRIMARY KEY, name TEXT UNIQUE)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO items(name) VALUES('duplicate')`); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO items(name) VALUES('duplicate')`)
	if !IsConstraint(err) {
		t.Fatalf("no-argument constraint error was not classified: %v", err)
	}
}

func TestFailedCommitRollsBackBeforeConnectionReuse(t *testing.T) {
	db, err := sql.Open("sqlite3", t.TempDir()+"/commit.db?_foreign_keys=on")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`
		CREATE TABLE parent(id INTEGER PRIMARY KEY);
		CREATE TABLE child(parent_id INTEGER, FOREIGN KEY(parent_id) REFERENCES parent(id) DEFERRABLE INITIALLY DEFERRED);
	`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO child(parent_id) VALUES(99)`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err == nil {
		t.Fatal("deferred foreign-key violation unexpectedly committed")
	}
	if _, err := db.Exec(`INSERT INTO parent(id) VALUES(1)`); err != nil {
		t.Fatalf("connection remained inside failed transaction: %v", err)
	}
}
