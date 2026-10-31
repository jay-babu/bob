// Package next is an experimental, schema-first SQL toolkit. Generated packages
// describe rows and columns; this package owns query construction and execution.
package next

import (
	"fmt"
	"slices"
)

// ColumnInfo describes database semantics independently of generated Go types.
type ColumnInfo struct {
	Name       string `json:"name"`
	DBType     string `json:"db_type,omitempty"`
	Nullable   bool   `json:"nullable,omitempty"`
	HasDefault bool   `json:"has_default,omitempty"`
	Generated  bool   `json:"generated,omitempty"`
}

// TableInfo is a portable metadata contract for generators and other tooling.
type TableInfo struct {
	Schema     string       `json:"schema,omitempty"`
	Name       string       `json:"name"`
	Columns    []ColumnInfo `json:"columns"`
	PrimaryKey []string     `json:"primary_key,omitempty"`
	UniqueKeys [][]string   `json:"unique_keys,omitempty"`
}

// Table has no references to related tables or row types.
type Table struct {
	info    TableInfo
	err     error
	columns map[string]ColumnInfo
}

func DefineTable(info TableInfo) *Table {
	t := &Table{info: cloneInfo(info), columns: make(map[string]ColumnInfo, len(info.Columns))}
	if info.Name == "" {
		t.err = fmt.Errorf("table name is empty")
	}
	seen := map[string]bool{}
	for _, c := range info.Columns {
		if c.Name == "" || seen[c.Name] {
			t.err = fmt.Errorf("empty or duplicate column in %s", info.Name)
		}
		seen[c.Name] = true
		t.columns[c.Name] = c
	}
	for _, key := range append([][]string{info.PrimaryKey}, info.UniqueKeys...) {
		keyColumns := map[string]bool{}
		for _, col := range key {
			if !seen[col] || keyColumns[col] {
				t.err = fmt.Errorf("unknown key column %s.%s", info.Name, col)
			}
			keyColumns[col] = true
		}
	}
	return t
}

func cloneInfo(i TableInfo) TableInfo {
	i.Columns = slices.Clone(i.Columns)
	i.PrimaryKey = slices.Clone(i.PrimaryKey)
	i.UniqueKeys = slices.Clone(i.UniqueKeys)
	for j := range i.UniqueKeys {
		i.UniqueKeys[j] = slices.Clone(i.UniqueKeys[j])
	}
	return i
}

func (t *Table) Info() TableInfo { return cloneInfo(t.info) }

func (t *Table) column(name string) (ColumnInfo, bool) {
	c, ok := t.columns[name]
	return c, ok
}

func (t *Table) unique(cols []string) bool {
	for _, key := range append([][]string{t.info.PrimaryKey}, t.info.UniqueKeys...) {
		if len(key) != len(cols) || len(key) == 0 {
			continue
		}
		match := true
		for _, c := range key {
			if !slices.Contains(cols, c) {
				match = false
			}
		}
		if match {
			return true
		}
	}
	return false
}

type binding struct {
	table *Table
	alias string
}

// Ref identifies one occurrence of a table in a query. Reuse the same Ref when
// composing fragments; create a second Ref for self-joins and subqueries.
type Ref struct{ b *binding }

func (t *Table) As(alias string) Ref { return Ref{&binding{t, alias}} }
func (r Ref) Table() *Table {
	if r.b == nil {
		return nil
	}
	return r.b.table
}

// Column carries the Go value type through comparisons and assignments. Query
// scope and SQL operator compatibility are validated separately.
type Column[T any] struct {
	ref  Ref
	name string
}

func Field[T any](ref Ref, name string) Column[T] { return Column[T]{ref, name} }
func (c Column[T]) Expr() Expr[T]                 { return Expr[T]{node: columnNode{c.ref, c.name}} }
func (c Column[T]) EQ(v T) Predicate              { return Equal(c.Expr(), Value(v)) }
func (c Column[T]) NE(v T) Predicate              { return compare(c.Expr().node, "<>", Value(v).node) }
func (c Column[T]) GT(v T) Predicate              { return compare(c.Expr().node, ">", Value(v).node) }
func (c Column[T]) GE(v T) Predicate              { return compare(c.Expr().node, ">=", Value(v).node) }
func (c Column[T]) LT(v T) Predicate              { return compare(c.Expr().node, "<", Value(v).node) }
func (c Column[T]) LE(v T) Predicate              { return compare(c.Expr().node, "<=", Value(v).node) }
func (c Column[T]) IsNull() Predicate             { return Predicate{unaryNode{c.Expr().node, "IS NULL"}} }
func (c Column[T]) IsNotNull() Predicate          { return Predicate{unaryNode{c.Expr().node, "IS NOT NULL"}} }
func (c Column[T]) In(values ...T) Predicate {
	nodes := make([]node, len(values))
	for i, v := range values {
		nodes[i] = valueNode{v}
	}
	return Predicate{inNode{c.Expr().node, nodes}}
}
func (c Column[T]) Select(label string) Selection { return Selection{c.Expr().node, label} }
func (c Column[T]) Asc() Order                    { return Order{c.Expr().node, false} }
func (c Column[T]) Desc() Order                   { return Order{c.Expr().node, true} }

// Relation is metadata, never an object navigation path. Composite foreign keys
// are kept as a single relation. It belongs in a package separate from row types.
type Relation struct {
	From, To               *Table
	FromColumns, ToColumns []string
}

func (r Relation) predicate(from, to Ref) (Predicate, error) {
	if r.From == nil || r.To == nil || from.Table() != r.From || to.Table() != r.To || len(r.FromColumns) == 0 || len(r.FromColumns) != len(r.ToColumns) {
		return Predicate{}, fmt.Errorf("invalid relationship endpoints or key arity")
	}
	ps := make([]Predicate, len(r.FromColumns))
	for i, c := range r.FromColumns {
		if _, ok := r.From.column(c); !ok {
			return Predicate{}, fmt.Errorf("unknown relationship source column %s", c)
		}
		if _, ok := r.To.column(r.ToColumns[i]); !ok {
			return Predicate{}, fmt.Errorf("unknown relationship target column %s", r.ToColumns[i])
		}
		ps[i] = compare(columnNode{from, c}, "=", columnNode{to, r.ToColumns[i]})
	}
	return And(ps...), nil
}

// Exists filters parents without multiplying them.
func (r Relation) Exists(from, to Ref, filters ...Predicate) Predicate {
	p, err := r.predicate(from, to)
	if err != nil {
		return Predicate{errorNode{err}}
	}
	q := Select(to, LiteralOne()).Where(append([]Predicate{p}, filters...)...)
	return Exists(q)
}

func (r Relation) Reverse() Relation {
	return Relation{r.To, r.From, slices.Clone(r.ToColumns), slices.Clone(r.FromColumns)}
}
