// Package gen implements compact, deterministic generation from a versioned
// schema document. The document is also the integration boundary for API tools.
package gen

import (
	"fmt"
	"go/parser"
	"go/token"
	"slices"
	"strings"
	"unicode"

	"github.com/stephenafamo/bob/next"
)

const Version = 1

type Schema struct {
	Version   int        `json:"version"`
	Tables    []Table    `json:"tables"`
	Relations []Relation `json:"relations,omitempty"`
}
type Table struct {
	Key     string         `json:"key"`
	Package string         `json:"package"`
	Info    next.TableInfo `json:"info"`
	Columns []Column       `json:"columns"`
	Imports []string       `json:"imports,omitempty"`
}
type Column struct {
	Name   string `json:"name"`
	GoName string `json:"go_name"`
	GoType string `json:"go_type"`
}
type Relation struct {
	Name        string   `json:"name"`
	From        string   `json:"from"`
	To          string   `json:"to"`
	FromColumns []string `json:"from_columns"`
	ToColumns   []string `json:"to_columns"`
}

// Config.Relations is an explicit allowlist. Metadata always retains every
// relationship, but row packages never depend on relationship packages.
type Config struct {
	Directory  string
	ImportPath string
	Relations  []string
}

func Identifier(s string) string {
	var b strings.Builder
	upper := true
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			upper = true
			continue
		}
		if b.Len() == 0 && unicode.IsDigit(r) {
			b.WriteByte('X')
		}
		if upper {
			r = unicode.ToUpper(r)
			upper = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

func validName(s string) bool {
	return token.IsIdentifier(s) && !token.Lookup(s).IsKeyword() && s != "_"
}

func (s Schema) Validate() error {
	if s.Version != Version {
		return fmt.Errorf("unsupported schema document version %d", s.Version)
	}
	keys := map[string]Table{}
	packages := map[string]bool{}
	for _, t := range s.Tables {
		if t.Key == "" || !validName(t.Package) || packages[t.Package] {
			return fmt.Errorf("invalid or duplicate package for table %s", t.Key)
		}
		if _, exists := keys[t.Key]; exists {
			return fmt.Errorf("duplicate table key %s", t.Key)
		}
		packages[t.Package] = true
		keys[t.Key] = t
		if t.Info.Name == "" || len(t.Columns) != len(t.Info.Columns) {
			return fmt.Errorf("table %s has incomplete column metadata", t.Key)
		}
		names := map[string]bool{}
		fields := map[string]bool{"Ref": true, "Projection": true, "Assignments": true}
		for i, c := range t.Columns {
			if c.Name == "" || c.Name != t.Info.Columns[i].Name || names[c.Name] || !validName(c.GoName) || !unicode.IsUpper([]rune(c.GoName)[0]) || fields[c.GoName] {
				return fmt.Errorf("invalid or conflicting column mapping %s.%s", t.Key, c.Name)
			}
			if _, err := parser.ParseExpr(c.GoType); err != nil || c.GoType == "" {
				return fmt.Errorf("invalid Go type for %s.%s", t.Key, c.Name)
			}
			names[c.Name] = true
			fields[c.GoName] = true
		}
		for _, key := range append([][]string{t.Info.PrimaryKey}, t.Info.UniqueKeys...) {
			seen := map[string]bool{}
			for _, c := range key {
				if !names[c] || seen[c] {
					return fmt.Errorf("invalid key in %s", t.Key)
				}
				seen[c] = true
			}
		}
	}
	rels := map[string]bool{}
	for _, r := range s.Relations {
		from, fromOK := keys[r.From]
		to, toOK := keys[r.To]
		if !validName(r.Name) || rels[r.Name] || !fromOK || !toOK || len(r.FromColumns) == 0 || len(r.FromColumns) != len(r.ToColumns) {
			return fmt.Errorf("invalid relationship %s", r.Name)
		}
		rels[r.Name] = true
		for i, c := range r.FromColumns {
			if !slices.ContainsFunc(from.Columns, func(x Column) bool { return x.Name == c }) || !slices.ContainsFunc(to.Columns, func(x Column) bool { return x.Name == r.ToColumns[i] }) {
				return fmt.Errorf("unknown relationship column in %s", r.Name)
			}
		}
	}
	return nil
}
