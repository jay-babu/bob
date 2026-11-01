// bobgen generates compact models from a versioned JSON schema document.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/stephenafamo/bob/next/gen"
)

func main() {
	source := flag.String("schema", "schema.json", "versioned schema document")
	directory := flag.String("out", "models", "generated package directory")
	importPath := flag.String("import", "", "Go import path of the output directory")
	relations := flag.String("relations", "", "comma-separated relationship allowlist")
	layout := flag.String("layout", gen.Flat, "flat or tables")
	flag.Parse()
	if err := run(*source, *directory, *importPath, *relations, *layout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(source, directory, importPath, relations string, layouts ...string) error {
	f, err := os.Open(source)
	if err != nil {
		return err
	}
	defer f.Close()
	var schema gen.Schema
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&schema); err != nil {
		return err
	}
	var allow []string
	if relations != "" {
		if relations == "all" {
			for _, relation := range schema.Relations {
				allow = append(allow, relation.Name)
			}
		} else {
			for _, name := range strings.Split(relations, ",") {
				allow = append(allow, strings.TrimSpace(name))
			}
		}
	}
	layout := gen.Flat
	if len(layouts) > 0 {
		layout = layouts[0]
	}
	report, err := gen.Generate(schema, gen.Config{Directory: directory, ImportPath: importPath, Relations: allow, Layout: layout})
	if err != nil {
		return err
	}
	fmt.Printf("generated: %d written, %d unchanged, %d removed; %d bytes\n", report.Written, report.Unchanged, report.Removed, report.Bytes)
	return nil
}
