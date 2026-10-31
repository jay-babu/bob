package driver_test

import (
	"testing"

	legacy "github.com/stephenafamo/bob/gen"
	"github.com/stephenafamo/bob/gen/drivers"
	"github.com/stephenafamo/bob/next/gen/driver"
)

func TestAdapterPreservesKeysAndExplicitTypeMappings(t *testing.T) {
	info := &drivers.DBInfo[any, any, any]{Tables: drivers.Tables[any, any]{{Key: "public.users", Schema: "public", Name: "users", Columns: []drivers.Column{{Name: "tenant", Type: "int64"}, {Name: "id", Type: "int64", AutoIncr: true}, {Name: "name", Type: "my.Name", Nullable: true}}, Constraints: drivers.Constraints[any]{Primary: &drivers.Constraint[any]{Columns: []string{"tenant", "id"}}}}}}
	var types drivers.Types
	types.Register("my.Name", drivers.Type{Imports: []string{`"unwanted.example/my"`}})
	s, err := driver.FromBob(info, types, map[string]driver.Mapping{"my.Name": {GoType: "string"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Tables[0].Imports) != 0 {
		t.Fatal("overridden type retained stale imports")
	}
	if len(s.Tables[0].Info.PrimaryKey) != 2 || !s.Tables[0].Info.Columns[1].HasDefault || !s.Tables[0].Info.Columns[2].Nullable {
		t.Fatal(s.Tables[0])
	}
}

func TestConfiguredAdapterAppliesTypesWithoutChangingInputColumns(t *testing.T) {
	info := &drivers.DBInfo[any, any, any]{Tables: drivers.Tables[any, any]{{Key: "interface", Name: "interface", Columns: []drivers.Column{{Name: "when", Type: "string"}}}}}
	name := "when"
	config := legacy.Config[any]{Types: map[string]drivers.Type{"time.Time": {Imports: []string{`"time"`}}}, Replacements: []legacy.Replace{{Match: legacy.ColumnFilter{Name: &name}, Replace: "time.Time"}}, Aliases: drivers.Aliases{"interface": {Columns: map[string]string{"when": "Timestamp"}}}}
	s, err := driver.Configured(info, drivers.Types{}, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Tables[0].Package != "interface_table" || s.Tables[0].Columns[0].GoType != "time.Time" || s.Tables[0].Columns[0].GoName != "Timestamp" {
		t.Fatal(s.Tables[0])
	}
	if info.Tables[0].Columns[0].Type != "string" {
		t.Fatal("input metadata changed")
	}
}
