package postgres_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stephenafamo/bob/next/gen"
	"github.com/stephenafamo/bob/next/gen/postgres"
	"github.com/stephenafamo/bob/next/internal/pgtest"
)

type counter struct {
	db      *sql.Tx
	queries int
}

func (c *counter) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	c.queries++
	return c.db.QueryContext(ctx, q, args...)
}

func TestCatalogAndGeneratedTypes(t *testing.T) {
	db := pgtest.Open(t)
	ctx := context.Background()
	for _, ddl := range []string{
		`CREATE SCHEMA inventory`,
		`CREATE TYPE inventory.status AS ENUM ('new','in-progress','NULL','value')`,
		`CREATE DOMAIN inventory.identifier AS text NOT NULL DEFAULT 'unset' CHECK (VALUE <> '')`,
		`CREATE TABLE inventory.thing(id inventory.identifier PRIMARY KEY,state inventory.status NOT NULL,prices numeric[] NOT NULL,tags inventory.status[],spans int4range, peer_id inventory.identifier, "row" text, "table" text, "columns" text, "insert" text, "query" text)`,
		`CREATE UNIQUE INDEX thing_state_unique ON inventory.thing(state) INCLUDE(prices)`,
		`CREATE TABLE inventory.peer(id inventory.identifier PRIMARY KEY,thing_id inventory.identifier REFERENCES inventory.thing(id) DEFERRABLE INITIALLY DEFERRED)`,
		`ALTER TABLE inventory.thing ADD FOREIGN KEY(peer_id) REFERENCES inventory.peer(id) DEFERRABLE INITIALLY DEFERRED`,
		`CREATE VIEW inventory.active AS SELECT id,state FROM inventory.thing`,
		`CREATE TABLE inventory.events(id bigint NOT NULL) PARTITION BY RANGE(id)`,
		`CREATE TABLE inventory.events_zero PARTITION OF inventory.events FOR VALUES FROM (0) TO (100)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	c := &counter{db: tx}
	schema, err := postgres.Inspect(ctx, c, postgres.Options{Schemas: []string{"inventory"}, ImportPath: "example.org/catalog/models"})
	if err != nil {
		t.Fatal(err)
	}
	if c.queries != 5 {
		t.Fatalf("catalog query count scales unexpectedly: %d", c.queries)
	}
	if len(schema.Tables) != 4 || len(schema.Relations) != 2 || len(schema.Types) != 3 {
		t.Fatalf("unexpected metadata: tables=%d relationships=%d types=%d", len(schema.Tables), len(schema.Relations), len(schema.Types))
	}
	for _, relation := range schema.Relations {
		if relation.Constraint == "" {
			t.Fatal("physical foreign-key identity is missing from schema metadata")
		}
	}
	for _, table := range schema.Tables {
		if table.Key == "inventory.thing" {
			if table.Info.Columns[0].Nullable || !table.Info.Columns[0].HasDefault || len(table.Info.UniqueKeys) != 1 {
				t.Fatal("domain or unique-index facts lost", table.Info)
			}
		}
		if table.Key == "inventory.active" && !table.Info.ReadOnly {
			t.Fatal("view is writable")
		}
	}
	for i := range schema.Tables {
		if schema.Tables[i].Key != "inventory.thing" {
			continue
		}
		schema.Tables[i].Imports = append(schema.Tables[i].Imports, `null "github.com/aarondl/opt/null"`)
		for j := range schema.Tables[i].Columns {
			if schema.Tables[i].Columns[j].Name == "query" {
				schema.Tables[i].Columns[j].NullableGoType = "null.Val[string]"
			}
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, layout := range []string{gen.Flat, gen.Tables} {
		if _, err := db.Exec(`TRUNCATE inventory.peer,inventory.thing`); err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		var relations []string
		for _, rel := range schema.Relations {
			relations = append(relations, rel.Name)
		}
		_, err = gen.Generate(schema, gen.Config{Layout: layout, Directory: filepath.Join(dir, "models"), ImportPath: "example.org/catalog/models", Relations: relations})
		if err != nil {
			t.Fatal(err)
		}
		viewPath := "models/inventory_active/table.bob.go"
		if layout == gen.Flat {
			viewPath = "models/inventory_active.bob.go"
		}
		view, err := os.ReadFile(filepath.Join(dir, viewPath))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(view), "type Insert") || strings.Contains(string(view), "next.Column[") {
			t.Fatal("generated view has write capabilities")
		}
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		root := filepath.Clean(filepath.Join(cwd, "../../.."))
		mod := fmt.Sprintf("module example.org/catalog\n\ngo 1.27.1\nrequire github.com/stephenafamo/bob v0.0.0\nreplace github.com/stephenafamo/bob => %q\n", root)
		if err = os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0o644); err != nil {
			t.Fatal(err)
		}
		source := `package catalog_test
import("context";"database/sql";"testing";_ "github.com/jackc/pgx/v5/stdlib";n "github.com/stephenafamo/bob/next";thing "example.org/catalog/models/inventory_thing";identifier "example.org/catalog/models/types/inventory_identifier";status "example.org/catalog/models/types/inventory_status";span "example.org/catalog/models/types/pg_catalog_int4range")
func TestGeneratedRoundTrip(t *testing.T){
db,err:=sql.Open("pgx",` + strconv.Quote(pgtest.DSN(t, db)) + `);if err!=nil{t.Fatal(err)};defer db.Close()
ctx:=context.Background();c:=thing.As("t");p:=c.Projection();decimal,err:=n.ParseDecimal("12.3400");if err!=nil{t.Fatal(err)}
key:=func(value string)identifier.Value{return identifier.Value{Domain:n.Domain[string]{V:value}}}
input:=thing.Insert{Id:n.Set(key("one")),State:status.New,Prices:n.Array[n.Decimal]{Elements:[]sql.Null[n.Decimal]{{V:decimal,Valid:true},{}}},Tags:n.Nullable(n.Set(n.Array[status.Value]{Elements:[]sql.Null[status.Value]{{V:status.InProgress,Valid:true},{}}})),Spans:n.Nullable(n.Set(span.Value("[1,5)"))),PeerId:n.Set(key("peer")),QueryValue:n.Nullable(n.Set("app-owned"))}
err=n.WithinTx(ctx,db,n.Postgres,nil,nil,func(tx n.Session)error{
if _,err:=tx.Exec(ctx,n.SQL("INSERT INTO inventory.peer(id,thing_id) VALUES($1,$2)","peer","one"));err!=nil{return err}
row,err:=n.One(ctx,tx,n.Insert(c.Ref,input.Assignments(c)...).Returning(p.Columns...),p);if err!=nil{return err}
if row.Id.V!="one"||row.State!=status.New||row.Prices.Elements[0].V.String()!="12.3400"||row.Prices.Elements[1].Valid||!row.Tags.Valid||row.Tags.V.Elements[0].V!=status.InProgress||row.Tags.V.Elements[1].Valid||row.Spans.V!=span.Value("[1,5)"){t.Fatal("generated SQL types changed in round trip")};return nil
});if err!=nil{t.Fatal(err)}
ctx=n.WithExecutor(ctx,db,n.ExecutionConfig{});row,err:=c.Query().Where(c.Id.EQ(key("one"))).OneContext(ctx);if err!=nil{t.Fatal(err)};if row.QueryValue.GetOrZero()!="app-owned"||row.State!=status.New{t.Fatal("full row query lost application value types")}
}
`
		if layout == gen.Flat {
			source = strings.ReplaceAll(source, "example.org/catalog/models/inventory_thing", "example.org/catalog/models")
			source = strings.ReplaceAll(source, "thing.As(", "thing.InventoryThingAs(")
			source = strings.ReplaceAll(source, "thing.Insert{", "thing.InventoryThingInsert{")
		}
		source = strings.ReplaceAll(source, `"one"`, strconv.Quote("one_"+layout))
		source = strings.ReplaceAll(source, `"peer"`, strconv.Quote("peer_"+layout))
		if err = os.WriteFile(filepath.Join(dir, "catalog_test.go"), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		// Compiling every generated package exercises domains, enum arrays, opaque
		// ranges, read-only views and both directions of the cyclic FK graph.
		compile, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		cmd := exec.CommandContext(compile, filepath.Join(runtime.GOROOT(), "bin", "go"), "test", "-mod=mod", "./...")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("generated schema does not compile: %s", out)
		}
	}
	all, err := postgres.Inspect(ctx, db, postgres.Options{Schemas: []string{"inventory"}, ImportPath: "example.org/catalog/models", IncludePartitions: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Tables) != 5 {
		t.Fatal("explicit partition selection ignored")
	}
}
