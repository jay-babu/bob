package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stephenafamo/bob/next/internal/pgtest"
)

func TestNativeDatabaseGeneration(t *testing.T) {
	db := pgtest.Open(t)
	if _, err := db.Exec(`CREATE TABLE item(id bigint PRIMARY KEY,name text NOT NULL,note text)`); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := run(context.Background(), pgtest.DSN(t, db), "public", dir, "example.org/models", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "public_item.bob.go")); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), pgtest.DSN(t, db), "public", dir, "example.org/models", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), "", "public", t.TempDir(), "example.org/models", "", ""); err == nil {
		t.Fatal("missing connection accepted")
	}
}
