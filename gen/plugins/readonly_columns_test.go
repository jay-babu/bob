package plugins

import (
	"testing"

	"github.com/stephenafamo/bob/gen"
	"github.com/stephenafamo/bob/gen/drivers"
)

func TestReadOnlyColumnsMarksConfiguredColumnsAsGenerated(t *testing.T) {
	plugin := ReadOnlyColumnsWithConfig[any, any, any](ReadOnlyColumnsConfig{
		Columns: []string{"changed_reason"},
		Tables: map[string][]string{
			"public.widgets": {"updated_by"},
		},
	})

	info := &drivers.DBInfo[any, any, any]{
		Tables: []drivers.Table[any, any]{
			{
				Key: "public.widgets",
				Columns: []drivers.Column{
					{Name: "id"},
					{Name: "changed_reason"},
					{Name: "updated_by"},
					{Name: "name"},
				},
			},
			{
				Key: "public.other_widgets",
				Columns: []drivers.Column{
					{Name: "changed_reason"},
					{Name: "updated_by"},
				},
			},
		},
	}

	if err := plugin.PlugDBInfo(info); err != nil {
		t.Fatal(err)
	}

	assertGenerated := func(table, column string, want bool) {
		t.Helper()

		for _, tbl := range info.Tables {
			if tbl.Key != table {
				continue
			}

			for _, col := range tbl.Columns {
				if col.Name == column {
					if col.Generated != want {
						t.Fatalf("%s.%s generated = %v, want %v", table, column, col.Generated, want)
					}
					return
				}
			}
		}

		t.Fatalf("column %s.%s not found", table, column)
	}

	assertGenerated("public.widgets", "changed_reason", true)
	assertGenerated("public.widgets", "updated_by", true)
	assertGenerated("public.widgets", "name", false)
	assertGenerated("public.other_widgets", "changed_reason", true)
	assertGenerated("public.other_widgets", "updated_by", false)
}

func TestReadOnlyColumnsImplementsDBInfoPlugin(t *testing.T) {
	var _ gen.DBInfoPlugin[any, any, any] = ReadOnlyColumnsWithConfig[any, any, any](ReadOnlyColumnsConfig{})
}
