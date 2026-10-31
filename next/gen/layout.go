package gen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"strings"
)

// Binding is the emitted Go location, independent of a table's logical namespace.
// Tooling consumes this instead of reverse-engineering generated source files.
type Binding struct {
	ImportPath string `json:"import_path"`
	Row        string `json:"row"`
	Table      string `json:"table"`
	Columns    string `json:"columns"`
	As         string `json:"as"`
	Insert     string `json:"insert,omitempty"`
	Update     string `json:"update,omitempty"`
}

// public is the implicit SQL schema in client-facing model names. Preserve
// custom namespaces and prefixes for every other schema.
func flatPrefix(table Table) string {
	name := table.Package
	if table.Info.Schema == "public" {
		name = strings.TrimPrefix(name, "public_")
	}
	return Identifier(name)
}

func binding(table Table, config Config) Binding {
	prefix, path := "", config.ImportPath+"/"+table.Package
	if config.Layout == Flat {
		prefix = flatPrefix(table)
		path = config.ImportPath
	}
	b := Binding{ImportPath: path, Row: prefix + "Row", Table: prefix + "Table", Columns: prefix + "Columns", As: prefix + "As"}
	if !table.Info.ReadOnly {
		b.Insert = prefix + "Insert"
		b.Update = prefix + "Update"
	}
	return b
}

// Rename Go declarations, never SQL strings, fields or selectors. Parsing is
// confined to the generator's own emitted declarations, not application source.
func flatSource(code []byte, pkg, prefix string) ([]byte, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "table.bob.go", code, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	objects := map[*ast.Object]string{}
	capture := func(id *ast.Ident) {
		if id.Obj == nil {
			err = fmt.Errorf("missing object for generated declaration %s", id.Name)
			return
		}
		objects[id.Obj] = prefix + id.Name
	}
	for _, decl := range file.Decls {
		switch decl := decl.(type) {
		case *ast.GenDecl:
			for _, spec := range decl.Specs {
				switch spec := spec.(type) {
				case *ast.TypeSpec:
					capture(spec.Name)
				case *ast.ValueSpec:
					for _, id := range spec.Names {
						capture(id)
					}
				}
			}
		case *ast.FuncDecl:
			if decl.Recv == nil {
				capture(decl.Name)
			}
		}
	}
	if err != nil {
		return nil, err
	}
	file.Name = ast.NewIdent(pkg)
	// The parser may resolve a keyed field (e.g. Projection.Columns) to the
	// current file's Columns type. A field key is never a package declaration.
	ast.Inspect(file, func(node ast.Node) bool {
		if pair, ok := node.(*ast.KeyValueExpr); ok {
			if key, ok := pair.Key.(*ast.Ident); ok {
				key.Obj = nil
			}
		}
		return true
	})
	ast.Inspect(file, func(node ast.Node) bool {
		if id, ok := node.(*ast.Ident); ok && id.Obj != nil {
			if name, ok := objects[id.Obj]; ok {
				id.Name = name
			}
		}
		return true
	})
	var result bytes.Buffer
	if err = format.Node(&result, fset, file); err != nil {
		return nil, err
	}
	return result.Bytes(), nil
}
