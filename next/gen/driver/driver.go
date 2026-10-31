// Package driver adapts Bob's existing database introspection into the compact
// schema contract. Database access stays in the existing, explicit driver.
package driver

import (
	"fmt"
	"go/token"
	"slices"
	"strings"

	legacy "github.com/stephenafamo/bob/gen"
	"github.com/stephenafamo/bob/gen/drivers"
	"github.com/stephenafamo/bob/gen/language"
	"github.com/stephenafamo/bob/next"
	"github.com/stephenafamo/bob/next/gen"
)

// Mapping supplies an application-owned Go type and its imports. Use it for
// schema enums and generator-output types that do not exist independently.
type Mapping struct {
	GoType  string   `json:"go_type"`
	Imports []string `json:"imports,omitempty"`
}

func FromBob[T, C, I any](info *drivers.DBInfo[T, C, I], types drivers.Types, overrides map[string]Mapping) (gen.Schema, error) {
	if info == nil {
		return gen.Schema{}, fmt.Errorf("database metadata is nil")
	}
	s := gen.Schema{Version: gen.Version}
	for _, table := range info.Tables {
		pkg := strings.ToLower(gen.Identifier(table.Key))
		if token.Lookup(pkg).IsKeyword() {
			pkg += "_table"
		}
		if pkg == "" {
			return s, fmt.Errorf("empty table package")
		}
		out := gen.Table{Key: table.Key, Package: pkg, Info: next.TableInfo{Schema: table.Schema, Name: table.Name}}
		if table.Constraints.Primary != nil {
			out.Info.PrimaryKey = table.Constraints.Primary.Columns
		}
		for _, unique := range table.Constraints.Uniques {
			out.Info.UniqueKeys = append(out.Info.UniqueKeys, unique.Columns)
		}
		imps := language.Languages{}.GetOutputLanguage(".go").Importer()
		for _, column := range table.Columns {
			name := gen.Identifier(column.Name)
			if name == "Ref" || name == "Projection" || name == "Assignments" {
				name += "Value"
			}
			var goType string
			if mapping, ok := overrides[column.Type]; ok {
				goType = mapping.GoType
				imps.ImportList(mapping.Imports)
			} else {
				goType = types.Get("", imps, column.Type)
			}
			if strings.Contains(goType, "output(") {
				return s, fmt.Errorf("type %s requires an explicit type mapping", column.Type)
			}
			out.Columns = append(out.Columns, gen.Column{Name: column.Name, GoName: name, GoType: goType})
			out.Info.Columns = append(out.Info.Columns, next.ColumnInfo{Name: column.Name, DBType: column.DBType, Nullable: column.Nullable, HasDefault: column.Default != "" || column.AutoIncr, Generated: column.Generated})
		}
		out.Imports = imps.ToList()
		slices.Sort(out.Imports)
		for _, imp := range out.Imports {
			if strings.Contains(imp, "output(") {
				return s, fmt.Errorf("table %s uses a generator-output type; supply an explicit type mapping", table.Key)
			}
		}
		s.Tables = append(s.Tables, out)
	}
	// An excluded target does not erase the source row or change its fields.
	keys := map[string]bool{}
	for _, table := range s.Tables {
		keys[table.Key] = true
	}
	for _, table := range info.Tables {
		for _, fk := range table.Constraints.Foreign {
			if !keys[fk.ForeignTable] {
				continue
			}
			name := strings.ToLower(gen.Identifier(table.Key + "_" + fk.Name))
			s.Relations = append(s.Relations, gen.Relation{Name: name, From: table.Key, To: fk.ForeignTable, FromColumns: fk.Columns, ToColumns: fk.ForeignColumns})
		}
	}
	return s, s.Validate()
}

// Configured applies existing column type replacements, extra constraints and
// column aliases before adapting. It rejects custom legacy relationship rules,
// which must be expressed in the versioned document instead of silently lost.
func Configured[T, C, I any](info *drivers.DBInfo[T, C, I], types drivers.Types, config legacy.Config[C], overrides map[string]Mapping) (gen.Schema, error) {
	if info == nil {
		return gen.Schema{}, fmt.Errorf("database metadata is nil")
	}
	if len(config.Relationships) > 0 {
		return gen.Schema{}, fmt.Errorf("custom legacy relationships require explicit schema-document relationships")
	}
	types.RegisterAll(config.Types)
	copyInfo := *info
	copyInfo.Tables = slices.Clone(info.Tables)
	for i := range copyInfo.Tables {
		t := &copyInfo.Tables[i]
		t.Columns = slices.Clone(t.Columns)
		if extra, ok := config.Constraints[t.Key]; ok {
			if extra.Primary != nil {
				t.Constraints.Primary = extra.Primary
			}
			t.Constraints.Foreign = append(slices.Clone(t.Constraints.Foreign), extra.Foreign...)
			t.Constraints.Uniques = append(slices.Clone(t.Constraints.Uniques), extra.Uniques...)
			t.Constraints.Checks = append(slices.Clone(t.Constraints.Checks), extra.Checks...)
		}
	}
	for _, replacement := range config.Replacements {
		for i := range copyInfo.Tables {
			t := &copyInfo.Tables[i]
			if len(replacement.Tables) > 0 && !slices.Contains(replacement.Tables, t.Key) {
				continue
			}
			for j := range t.Columns {
				if replacement.Match.Matches(t.Columns[j]) {
					t.Columns[j].Type = replacement.Replace
				}
			}
		}
	}
	s, err := FromBob(&copyInfo, types, overrides)
	if err != nil {
		return s, err
	}
	for i := range s.Tables {
		aliases := config.Aliases[s.Tables[i].Key]
		for j := range s.Tables[i].Columns {
			if alias, ok := aliases.Columns[s.Tables[i].Columns[j].Name]; ok {
				s.Tables[i].Columns[j].GoName = alias
			}
		}
	}
	return s, s.Validate()
}
