package next_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	n "github.com/stephenafamo/bob/next"
	"github.com/stephenafamo/bob/next/internal/pgtest"
)

func TestJSONTextPreservesPostgresOperatorAndBoundKeys(t *testing.T) {
	table := n.DefineTable(n.TableInfo{Name: "documents", PrimaryKey: []string{"id"}, Columns: []n.ColumnInfo{{Name: "id", DBType: "bigint"}, {Name: "metadata", DBType: "jsonb"}}})
	ref := table.As("document")
	metadata := n.Field[json.RawMessage](ref, "metadata")
	query := n.Select(ref, n.Field[int64](ref, "id").Select("id")).Where(n.JSONText(metadata.Expr(), "external_id").EQ("chosen"))
	statement := build(t, query, n.Postgres)
	require(t, strings.Contains(statement.SQL, " ->> $1"), true)
	require(t, statement.Args, []any{"external_id", "chosen"})
	if _, err := query.Build(n.SQLite); err == nil {
		t.Fatal("PostgreSQL JSON operator accepted by SQLite")
	}
	db := pgtest.Open(t)
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE documents(id bigint PRIMARY KEY,metadata jsonb);INSERT INTO documents VALUES(1,'{"external_id":"chosen"}'),(2,'{}')`); err != nil {
		t.Fatal(err)
	}
	projection := n.Projection[int64]{Columns: []n.Selection{n.Field[int64](ref, "id").Select("id")}, Scan: func(s n.Scanner) (int64, error) { var id int64; err := s.Scan(&id); return id, err }}
	ctx := n.WithExecutor(context.Background(), db, n.ExecutionConfig{})
	result, err := n.OneContext(ctx, query, projection)
	require(t, err, nil)
	require(t, result, int64(1))
	bad := n.Select(ref, n.Field[int64](ref, "id").Select("id")).Where(n.JSONText(n.Field[int64](ref, "id").Expr(), "key").EQ("x"))
	if _, err := bad.Build(n.Postgres); err == nil {
		t.Fatal("non-JSON column accepted")
	}
}
