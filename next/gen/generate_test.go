package gen_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stephenafamo/bob/next"
	"github.com/stephenafamo/bob/next/gen"
)

func TestPublicSchemaIsImplicitInFlatModelNames(t *testing.T) {
	s := schema()
	s.Tables[0].Key = "public.users"
	s.Tables[0].Package = "public_users"
	s.Tables[0].Info.Schema = "public"
	other := s.Tables[0]
	other.Key, other.Package, other.Info.Schema = "audit.users", "audit_users", "audit"
	s.Tables = append(s.Tables, other)
	s.Relations = []gen.Relation{{Name: "audit_user", From: "audit.users", To: "public.users", FromColumns: []string{"id"}, ToColumns: []string{"id"}}}
	dir := t.TempDir()
	if _, err := gen.Generate(s, gen.Config{Directory: dir, ImportPath: "example.org/models", Relations: []string{"audit_user"}}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "go-bindings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Tables map[string]gen.Binding `json:"tables"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	bindings := document.Tables
	if b := bindings["public.users"]; b.Row != "UsersRow" || b.As != "UsersAs" || b.Columns != "UsersColumns" || b.Table != "UsersTable" || b.Insert != "UsersInsert" || b.Update != "UsersUpdate" {
		t.Fatal("public schema leaked into client names", b)
	}
	if b := bindings["audit.users"]; b.Row != "AuditUsersRow" {
		t.Fatal("non-public schema prefix was lost", b)
	}
	code, err := os.ReadFile(filepath.Join(dir, "public_users.bob.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(code), "PublicUsers") || !strings.Contains(string(code), `Schema: "public"`) || !strings.Contains(string(code), "func UsersAs(") {
		t.Fatal("generated names or SQL schema are incorrect", string(code))
	}
	code, err = os.ReadFile(filepath.Join(dir, "relations/audit_user/relation.bob.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(code), "from.AuditUsersTable") || !strings.Contains(string(code), "from.UsersTable") {
		t.Fatal("relationship did not use emitted table names", string(code))
	}
	// Omitting public must not silently merge conflicting Go namespaces.
	s.Tables[1].Package = "users"
	if _, err := gen.Generate(s, gen.Config{Directory: t.TempDir(), ImportPath: "example.org/models"}); err == nil {
		t.Fatal("colliding model names were accepted")
	}
}

func schema() gen.Schema {
	return gen.Schema{Version: gen.Version, Tables: []gen.Table{{Key: "users", Package: "users", Info: next.TableInfo{Name: "users", PrimaryKey: []string{"id"}, Columns: []next.ColumnInfo{{Name: "id", HasDefault: true}, {Name: "name"}, {Name: "note", Nullable: true}, {Name: "computed", Generated: true}}}, Columns: []gen.Column{{Name: "id", GoName: "ID", GoType: "int64"}, {Name: "name", GoName: "Name", GoType: "string"}, {Name: "note", GoName: "Note", GoType: "string"}, {Name: "computed", GoName: "Computed", GoType: "int64"}}}}}
}

func TestApplicationValueTypesAndModelNames(t *testing.T) {
	s := schema()
	s.Tables[0].GoName = "Account"
	s.Tables[0].Columns[2].NullableGoType = "*string"
	dir := t.TempDir()
	if _, err := gen.Generate(s, gen.Config{Directory: dir, ImportPath: "example.org/models"}); err != nil {
		t.Fatal(err)
	}
	code, err := os.ReadFile(filepath.Join(dir, "users.bob.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(code), "type AccountRow struct") || !strings.Contains(strings.Join(strings.Fields(string(code)), " "), "Note *string") || strings.Contains(string(code), "sql.Null[") {
		t.Fatal(string(code))
	}
	// An invalid override must be rejected before publishing any output.
	s.Tables[0].Columns[2].NullableGoType = "not a type"
	if _, err := gen.Generate(s, gen.Config{Directory: dir, ImportPath: "example.org/models"}); err == nil {
		t.Fatal("invalid nullable Go type accepted")
	}
}
func TestIncrementalGenerationOwnershipAndCleanup(t *testing.T) {
	dir := t.TempDir()
	config := gen.Config{Layout: gen.Tables, Directory: dir, ImportPath: "example.org/models"}
	s := schema()
	report, err := gen.Generate(s, config)
	if err != nil {
		t.Fatal(err)
	}
	if report.Written != 3 {
		t.Fatal(report)
	}
	path := filepath.Join(dir, "users/table.bob.go")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	report, err = gen.Generate(s, config)
	if err != nil {
		t.Fatal(err)
	}
	if report.Written != 0 || report.Unchanged != 3 {
		t.Fatal(report)
	}
	after, _ := os.Stat(path)
	if before.ModTime() != after.ModTime() {
		t.Fatal("unchanged model rewritten")
	}
	code, _ := os.ReadFile(path)
	text := string(code)
	if !strings.Contains(text, "sql.Null[string]") || !strings.Contains(text, "Name string") || !strings.Contains(text, "next.NullableInput[string]") {
		t.Fatal(text)
	}
	// Adding an unrelated table must not rewrite existing packages.
	extra := s.Tables[0]
	extra.Key = "other"
	extra.Package = "other"
	extra.Info.Name = "other"
	s.Tables = append(s.Tables, extra)
	if _, err = gen.Generate(s, config); err != nil {
		t.Fatal(err)
	}
	after, _ = os.Stat(path)
	if before.ModTime() != after.ModTime() {
		t.Fatal("unrelated schema change rewrote a model")
	}
	s.Tables = s.Tables[1:]
	if err = os.WriteFile(filepath.Join(dir, "manual.go"), []byte("package manual\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err = gen.Generate(s, config)
	if err != nil {
		t.Fatal(err)
	}
	if report.Removed != 1 {
		t.Fatal(report)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("stale model retained")
	}
	if _, err = os.Stat(filepath.Join(dir, "manual.go")); err != nil {
		t.Fatal("unowned file deleted")
	}
	other := filepath.Join(dir, "other/table.bob.go")
	if err = os.WriteFile(other, []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = gen.Generate(s, config); err == nil {
		t.Fatal("edited generated file overwritten")
	}
}

func TestRelationshipFanoutDoesNotChangeRows(t *testing.T) {
	s := schema()
	dir := t.TempDir()
	config := gen.Config{Layout: gen.Tables, Directory: dir, ImportPath: "example.org/models"}
	if _, err := gen.Generate(s, config); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "users/table.bob.go")
	before, _ := os.ReadFile(path)
	for i := 0; i < 150; i++ {
		name := "referencing" + strings.Repeat("x", i+1)
		table := gen.Table{Key: name, Package: name, Info: next.TableInfo{Name: name, Columns: []next.ColumnInfo{{Name: "user_id"}}}, Columns: []gen.Column{{Name: "user_id", GoName: "UserID", GoType: "int64"}}}
		s.Tables = append(s.Tables, table)
		s.Relations = append(s.Relations, gen.Relation{Name: name, From: name, To: "users", FromColumns: []string{"user_id"}, ToColumns: []string{"id"}})
	}
	if _, err := gen.Generate(s, config); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("reverse fanout changed target row package")
	}
	if _, err := os.Stat(filepath.Join(dir, "relations")); !os.IsNotExist(err) {
		t.Fatal("relationships generated without allowlist")
	}
	config.Relations = []string{s.Relations[0].Name}
	if _, err := gen.Generate(s, config); err != nil {
		t.Fatal(err)
	}
	code, err := os.ReadFile(filepath.Join(dir, "relations", config.Relations[0], "relation.bob.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(code), "example.org/models/users") {
		t.Fatal(string(code))
	}
	config.Relations = []string{"missing"}
	if _, err := gen.Generate(s, config); err == nil {
		t.Fatal("missing allowlisted relation accepted")
	}
}

func TestSchemaValidationAndPreflight(t *testing.T) {
	for _, modify := range []func(*gen.Schema){
		func(s *gen.Schema) { s.Version = 100 },
		func(s *gen.Schema) { s.Tables[0].Package = "../escape" },
		func(s *gen.Schema) { s.Tables[0].Columns[0].GoName = "Ref" },
		func(s *gen.Schema) { s.Tables[0].Columns[0].GoType = "invalid type !" },
		func(s *gen.Schema) { s.Tables[0].Info.PrimaryKey = []string{"absent"} },
	} {
		s := schema()
		modify(&s)
		if s.Validate() == nil {
			t.Fatal("invalid schema accepted")
		}
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "users"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "users/table.bob.go")
	if err := os.WriteFile(path, []byte("user owned"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := gen.Generate(schema(), gen.Config{Layout: gen.Tables, Directory: dir, ImportPath: "example.org/models"}); err == nil {
		t.Fatal("unowned file overwritten")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "user owned" {
		t.Fatal("preflight changed file")
	}
}

func TestInterruptedPublicationCanBeRepaired(t *testing.T) {
	s := schema()
	dir := t.TempDir()
	config := gen.Config{Layout: gen.Tables, Directory: dir, ImportPath: "example.org/models"}
	if _, err := gen.Generate(s, config); err != nil {
		t.Fatal(err)
	}
	s.Tables[0].Info.Columns = append(s.Tables[0].Info.Columns, next.ColumnInfo{Name: "added"})
	s.Tables[0].Columns = append(s.Tables[0].Columns, gen.Column{Name: "added", GoName: "Added", GoType: "string"})
	proposal := t.TempDir()
	if _, err := gen.Generate(s, gen.Config{Layout: gen.Tables, Directory: proposal, ImportPath: config.ImportPath}); err != nil {
		t.Fatal(err)
	}
	// Publish one file, leaving the old manifest as an interrupted run would.
	data, err := os.ReadFile(filepath.Join(proposal, "users/table.bob.go"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "users/table.bob.go"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = gen.Generate(s, config); err != nil {
		t.Fatal(err)
	}
	if _, err = gen.Generate(s, config); err != nil {
		t.Fatal(err)
	}
}

func TestCustomSQLTypesShareNullableImport(t *testing.T) {
	s := schema()
	s.Tables[0].Columns[2].GoType = "dbsql.RawBytes"
	s.Tables[0].Imports = []string{`dbsql "database/sql"`}
	dir := t.TempDir()
	if _, err := gen.Generate(s, gen.Config{Layout: gen.Tables, Directory: dir, ImportPath: "example.org/models"}); err != nil {
		t.Fatal(err)
	}
	code, err := os.ReadFile(filepath.Join(dir, "users/table.bob.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(code), `"database/sql"`) != 1 || !strings.Contains(string(code), "dbsql.Null[dbsql.RawBytes]") {
		t.Fatal(string(code))
	}
}

func TestSwitchingLayoutsOwnsOnlyGeneratedFiles(t *testing.T) {
	dir := t.TempDir()
	s := schema()
	config := gen.Config{Directory: dir, ImportPath: "example.org/models"}
	if _, err := gen.Generate(s, config); err != nil {
		t.Fatal(err)
	}
	flat := filepath.Join(dir, "users.bob.go")
	if _, err := os.Stat(flat); err != nil {
		t.Fatal(err)
	}
	config.Layout = gen.Tables
	if _, err := gen.Generate(s, config); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(flat); !os.IsNotExist(err) {
		t.Fatal("old flat output survived a layout switch")
	}
	if _, err := os.Stat(filepath.Join(dir, "users/table.bob.go")); err != nil {
		t.Fatal(err)
	}
	config.Layout = gen.Flat
	if _, err := gen.Generate(s, config); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(flat); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "users/table.bob.go")); !os.IsNotExist(err) {
		t.Fatal("old table output survived a layout switch")
	}
}
