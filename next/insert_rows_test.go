package next

import (
	"reflect"
	"strings"
	"testing"
)

func TestInsertRowsPreservesDefaultsAndRowValidation(t *testing.T) {
	table := DefineTable(TableInfo{Name: "items", Columns: []ColumnInfo{{Name: "id"}, {Name: "note", Nullable: true}, {Name: "version", HasDefault: true}, {Name: "computed", Generated: true}}})
	r := table.As("i")
	id := Field[int64](r, "id")
	note := Field[string](r, "note")
	q := InsertRows(r, []Assignment{id.Set(1), note.Set("first")}, []Assignment{id.Set(2)})
	statement, err := q.Build(Postgres)
	if err != nil {
		t.Fatal(err)
	}
	if statement.SQL != `INSERT INTO "items" ("id", "note") VALUES ($1, $2), ($3, DEFAULT)` || !reflect.DeepEqual(statement.Args, []any{int64(1), "first", int64(2)}) {
		t.Fatalf("unexpected statement: %#v", statement)
	}
	for _, rows := range [][][]Assignment{nil, {{note.Set("missing id")}}, {{id.Set(1), id.Set(2)}}, {{Field[int64](table.As("other"), "id").Set(1)}}, {{id.Set(1), Field[int64](r, "computed").Set(3)}}} {
		if _, err := InsertRows(r, rows...).Build(Postgres); err == nil {
			t.Fatalf("accepted invalid rows: %#v", rows)
		}
	}
	if _, err := q.Build(SQLite); err == nil || !strings.Contains(err.Error(), "DEFAULT") {
		t.Fatalf("expected SQLite mixed-default error, got %v", err)
	}
}
