// bobgen-db introspects a configured database and generates compact table models.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	_ "github.com/jackc/pgx/v5/stdlib"
	legacy "github.com/stephenafamo/bob/gen"
	helpers "github.com/stephenafamo/bob/gen/bobgen-helpers"
	mysql "github.com/stephenafamo/bob/gen/bobgen-mysql/driver"
	psql "github.com/stephenafamo/bob/gen/bobgen-psql/driver"
	sqlite "github.com/stephenafamo/bob/gen/bobgen-sqlite/driver"
	"github.com/stephenafamo/bob/gen/drivers"
	"github.com/stephenafamo/bob/next/gen"
	adapter "github.com/stephenafamo/bob/next/gen/driver"
)

func main() {
	config := flag.String("config", "bobgen.yaml", "existing database driver configuration")
	dialect := flag.String("dialect", "psql", "psql, mysql, or sqlite")
	out := flag.String("out", "next-models", "new output directory")
	importPath := flag.String("import", "", "Go import path of the output directory")
	typeMap := flag.String("type-map", "", "optional JSON map of source Go types to explicit mappings")
	relations := flag.String("relations", "", "comma-separated relationship allowlist")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, *config, *dialect, *out, *importPath, *typeMap, *relations); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, config, dialect, out, importPath, typeMap, relations string) error {
	if out == "" || importPath == "" {
		return fmt.Errorf("output directory and import path are required")
	}
	overrides := map[string]adapter.Mapping{}
	if typeMap != "" {
		data, err := os.ReadFile(typeMap)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(data, &overrides); err != nil {
			return err
		}
	}
	var schema gen.Schema
	var err error
	switch dialect {
	case "psql":
		cfg, db, _, e := helpers.GetConfigFromFile[any, psql.Config](config, "psql")
		if e != nil {
			return e
		}
		schema, err = assemble(ctx, psql.New(db), cfg, overrides)
	case "mysql":
		cfg, db, _, e := helpers.GetConfigFromFile[any, mysql.Config](config, "mysql")
		if e != nil {
			return e
		}
		schema, err = assemble(ctx, mysql.New(db), cfg, overrides)
	case "sqlite":
		cfg, db, _, e := helpers.GetConfigFromFile[any, sqlite.Config](config, "sqlite")
		if e != nil {
			return e
		}
		schema, err = assemble(ctx, sqlite.New(db), cfg, overrides)
	default:
		return fmt.Errorf("unsupported introspection dialect %s", dialect)
	}
	if err != nil {
		return err
	}
	var allow []string
	if relations != "" {
		for _, name := range strings.Split(relations, ",") {
			allow = append(allow, strings.TrimSpace(name))
		}
	}
	report, err := gen.Generate(schema, gen.Config{Directory: out, ImportPath: importPath, Relations: allow})
	if err != nil {
		return err
	}
	fmt.Printf("generated: %d written, %d unchanged, %d removed; %d bytes\n", report.Written, report.Unchanged, report.Removed, report.Bytes)
	return nil
}

func assemble[T, C, I any](ctx context.Context, d drivers.Interface[T, C, I], config legacy.Config[C], overrides map[string]adapter.Mapping) (gen.Schema, error) {
	info, err := d.Assemble(ctx)
	if err != nil {
		return gen.Schema{}, err
	}
	return adapter.Configured(info, d.Types(), config, overrides)
}
