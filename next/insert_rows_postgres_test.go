package next_test

import (
	"database/sql"
	"testing"

	n "github.com/stephenafamo/bob/next"
	"github.com/stephenafamo/bob/next/internal/pgtest"
)

func TestPostgreSQLBulkInsertUsesDefaultsAndRollsBack(t *testing.T) {
	db := pgtest.Open(t)
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE bulk_rows(id bigint PRIMARY KEY,note text DEFAULT 'server default',fingerprint bigint GENERATED ALWAYS AS (id*2) STORED)`); err != nil {
		t.Fatal(err)
	}
	table := n.DefineTable(n.TableInfo{Name: "bulk_rows", PrimaryKey: []string{"id"}, Columns: []n.ColumnInfo{{Name: "id"}, {Name: "note", Nullable: true, HasDefault: true}, {Name: "fingerprint", Generated: true}}})
	r := table.As("r")
	id := n.Field[int64](r, "id")
	note := n.Field[string](r, "note")
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	ctx := n.WithExecutor(t.Context(), tx, n.ExecutionConfig{})
	count, err := n.ExecContext(ctx, n.InsertRows(r, []n.Assignment{id.Set(1), note.Set("first")}, []n.Assignment{id.Set(2)}, []n.Assignment{id.Set(3), note.AssignNullable(n.Null[string]())}))
	if err != nil {
		t.Fatal(err)
	}
	require(t, count, int64(3))
	rows, err := tx.QueryContext(t.Context(), `SELECT note,fingerprint FROM bulk_rows ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := []sql.NullString{{String: "first", Valid: true}, {String: "server default", Valid: true}, {}}
	for i := 0; rows.Next(); i++ {
		var value sql.NullString
		var fingerprint int64
		if err := rows.Scan(&value, &fingerprint); err != nil {
			t.Fatal(err)
		}
		require(t, value, want[i])
		require(t, fingerprint, int64((i+1)*2))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var persisted int64
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM bulk_rows`).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	require(t, persisted, int64(0))
}
