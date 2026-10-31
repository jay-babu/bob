// compare generates the same synthetic high-fanout schema through the existing
// generator and the compact generator. It never connects to a database.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	legacy "github.com/stephenafamo/bob/gen"
	"github.com/stephenafamo/bob/gen/drivers"
	"github.com/stephenafamo/bob/gen/plugins"
	"github.com/stephenafamo/bob/next"
	compact "github.com/stephenafamo/bob/next/gen"
)

type fixtureDriver struct {
	tables, columns int
	changed         bool
}

func (f fixtureDriver) Dialect() string { return "psql" }
func (f fixtureDriver) Types() drivers.Types {
	var types drivers.Types
	types.Register("int64", drivers.Type{})
	types.Register("string", drivers.Type{})
	return types
}
func (f fixtureDriver) Assemble(context.Context) (*drivers.DBInfo[any, any, any], error) {
	info := &drivers.DBInfo[any, any, any]{}
	for i := 0; i < f.tables; i++ {
		name := "owner"
		if i > 0 {
			name = fmt.Sprintf("child_%03d", i)
		}
		t := drivers.Table[any, any]{Key: "public." + name, Schema: "public", Name: name, Columns: []drivers.Column{{Name: "id", Type: "int64", DBType: "bigint"}}, Constraints: drivers.Constraints[any]{Primary: &drivers.Constraint[any]{Name: name + "_pk", Columns: []string{"id"}}}}
		if i > 0 {
			t.Columns = append(t.Columns, drivers.Column{Name: "owner_id", Type: "int64", DBType: "bigint"})
			t.Constraints.Foreign = []drivers.ForeignKey[any]{{Constraint: drivers.Constraint[any]{Name: name + "_owner_fk", Columns: []string{"owner_id"}}, ForeignTable: "public.owner", ForeignColumns: []string{"id"}}}
		}
		for j := len(t.Columns); j < f.columns; j++ {
			t.Columns = append(t.Columns, drivers.Column{Name: fmt.Sprintf("field_%02d", j), Type: "string", DBType: "text"})
		}
		if f.changed && i == f.tables-1 {
			t.Columns = append(t.Columns, drivers.Column{Name: "added_field", Type: "string", DBType: "text"})
		}
		info.Tables = append(info.Tables, t)
	}
	return info, nil
}

func main() {
	dir := flag.String("out", "", "fresh benchmark output directory")
	root := flag.String("bob", "", "absolute path to Bob checkout")
	count := flag.Int("tables", 150, "number of tables including the referenced owner")
	columns := flag.Int("columns", 16, "columns per table")
	changed := flag.Bool("change", false, "add a field to one child table")
	flag.Parse()
	if err := run(*dir, *root, *count, *columns, *changed); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(dir, root string, count, columns int, changed bool) error {
	if dir == "" || root == "" || count < 2 || columns < 2 {
		return fmt.Errorf("out, bob, at least two tables and columns are required")
	}
	if !filepath.IsAbs(dir) || !filepath.IsAbs(root) {
		return fmt.Errorf("output and Bob paths must be absolute")
	}
	f := fixtureDriver{count, columns, changed}
	for _, mode := range []string{"legacy", "compact"} {
		folder := filepath.Join(dir, mode)
		if err := os.MkdirAll(folder, 0o755); err != nil {
			return err
		}
		mod := "module example.org/bobbench\n\ngo 1.27.1\n\nrequire github.com/stephenafamo/bob v0.0.0\nreplace github.com/stephenafamo/bob => " + strconv.Quote(root) + "\n"
		if _, err := os.Stat(filepath.Join(folder, "go.mod")); os.IsNotExist(err) {
			if err := os.WriteFile(filepath.Join(folder, "go.mod"), []byte(mod), 0o644); err != nil {
				return err
			}
		}
		out := filepath.Join(folder, "models")
		if mode == "legacy" {
			no := false
			yes := true
			config := plugins.PresetNone
			config.Models = plugins.OutputConfig{Destination: out, Pkgname: "models", Disabled: &no}
			config.Enums = plugins.OutputConfig{Destination: filepath.Join(folder, "enums"), Pkgname: "enums", Disabled: &no}
			config.Factory = plugins.OutputConfig{Destination: filepath.Join(folder, "factory"), Pkgname: "factory", Disabled: &yes}
			if err := os.MkdirAll(config.Factory.Destination, 0o755); err != nil {
				return err
			}
			config.Where.Disabled = &no
			config.Loaders.Disabled = &no
			config.Joins.Disabled = &no
			config.Counts.Disabled = &no
			state := &legacy.State[any]{Config: legacy.Config[any]{NoTests: true, SliceMutationMethods: &no, NoBackReferencing: yes, TypeSystem: "database/sql"}}
			if err := legacy.Run(context.Background(), state, f, plugins.Setup[any, any, any](config, legacy.PSQLTemplates)...); err != nil {
				return err
			}
			main := `package main
import("context";"database/sql";"fmt";"github.com/stephenafamo/bob"; owner "example.org/bobbench/models/public/owner")
func main(){var db *sql.DB; rows,err:=owner.PublicOwners.Query().All(context.Background(),bob.NewDB(db));fmt.Println(rows,err)}
`
			if _, err := os.Stat(filepath.Join(folder, "main.go")); os.IsNotExist(err) {
				if err := os.WriteFile(filepath.Join(folder, "main.go"), []byte(main), 0o644); err != nil {
					return err
				}
			}
		} else {
			info, _ := f.Assemble(context.Background())
			s := compact.Schema{Version: compact.Version}
			for _, table := range info.Tables {
				t := compact.Table{Key: table.Key, Package: strings.ToLower(compact.Identifier(table.Key)), Info: next.TableInfo{Schema: table.Schema, Name: table.Name, PrimaryKey: table.Constraints.Primary.Columns}}
				for _, column := range table.Columns {
					t.Info.Columns = append(t.Info.Columns, next.ColumnInfo{Name: column.Name, DBType: column.DBType})
					t.Columns = append(t.Columns, compact.Column{Name: column.Name, GoName: compact.Identifier(column.Name), GoType: column.Type})
				}
				s.Tables = append(s.Tables, t)
			}
			if _, err := compact.Generate(s, compact.Config{Layout: compact.Tables, Directory: out, ImportPath: "example.org/bobbench/models"}); err != nil {
				return err
			}
			main := `package main
import("context";"database/sql";"fmt";"github.com/stephenafamo/bob/next";owner "example.org/bobbench/models/publicowner")
func main(){var db *sql.DB;c:=owner.As("o");p:=c.Projection();rows,err:=next.All(context.Background(),next.NewSession(db,next.Postgres,nil),next.Select(c.Ref,p.Columns...),p);fmt.Println(rows,err)}
`
			if _, err := os.Stat(filepath.Join(folder, "main.go")); os.IsNotExist(err) {
				if err := os.WriteFile(filepath.Join(folder, "main.go"), []byte(main), 0o644); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
