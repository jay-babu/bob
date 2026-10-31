package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDatabaseIntrospectionToCompactModels(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE item (id INTEGER NOT NULL PRIMARY KEY, name TEXT NOT NULL, note TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "bobgen.yaml")
	text := "sqlite:\n  dsn: " + dbPath + "\naliases:\n  item:\n    columns:\n      name: DisplayName\n"
	if err = os.WriteFile(config, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "models")
	if err = run(context.Background(), config, "sqlite", out, "example.org/models", "", ""); err != nil {
		t.Fatal(err)
	}
	code, err := os.ReadFile(filepath.Join(out, "item/table.bob.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(code), "DisplayName") || !strings.Contains(string(code), "sql.Null[string]") {
		t.Fatal(string(code))
	}
	if err = run(context.Background(), config, "sqlite", out, "example.org/models", "", ""); err != nil {
		t.Fatal(err)
	}
}
