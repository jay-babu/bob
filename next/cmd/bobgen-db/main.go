// bobgen-db generates compact models directly from PostgreSQL catalogs.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stephenafamo/bob/next/gen"
	"github.com/stephenafamo/bob/next/gen/postgres"
)

func main() {
	dsnEnv := flag.String("dsn-env", "BOB_NEXT_DSN", "environment variable containing the PostgreSQL connection string")
	schemas := flag.String("schemas", "public", "comma-separated schema allowlist")
	out := flag.String("out", "models", "generated output directory")
	importPath := flag.String("import", "", "Go import path of the output directory")
	typeMap := flag.String("type-map", "", "JSON mappings keyed by qualified PostgreSQL type name")
	relations := flag.String("relations", "", "comma-separated relationship allowlist, or all")
	layout := flag.String("layout", gen.Flat, "flat or tables")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Getenv(*dsnEnv), *schemas, *out, *importPath, *typeMap, *relations, *layout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, dsn, schemas, out, importPath, typeMap, relations string, layouts ...string) error {
	if dsn == "" || out == "" || importPath == "" {
		return fmt.Errorf("database connection, output directory and import path are required")
	}
	overrides := map[string]postgres.Mapping{}
	if typeMap != "" {
		data, err := os.ReadFile(typeMap)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(data, &overrides); err != nil {
			return err
		}
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("invalid database connection configuration")
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return fmt.Errorf("cannot start schema inspection transaction")
	}
	defer func() { _ = tx.Rollback() }()
	schema, err := postgres.Inspect(ctx, tx, postgres.Options{Schemas: split(schemas), ImportPath: importPath, Types: overrides})
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	allow := split(relations)
	if relations == "all" {
		allow = nil
		for _, relation := range schema.Relations {
			allow = append(allow, relation.Name)
		}
	}
	layout := gen.Flat
	if len(layouts) > 0 {
		layout = layouts[0]
	}
	report, err := gen.Generate(schema, gen.Config{Directory: out, ImportPath: importPath, Relations: allow, Layout: layout})
	if err != nil {
		return err
	}
	fmt.Printf("generated: %d written, %d unchanged, %d removed; %d bytes\n", report.Written, report.Unchanged, report.Removed, report.Bytes)
	return nil
}

func split(value string) []string {
	var values []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			values = append(values, item)
		}
	}
	return values
}
