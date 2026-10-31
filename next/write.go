package next

import (
	"fmt"
	"slices"
)

type inputKind uint8

const (
	omitted inputKind = iota
	assigned
	defaulted
)

// Input distinguishes an omitted write from an explicit value (including zero).
// Null is available only through NullableInput in generated inputs.
type Input[T any] struct {
	kind  inputKind
	value node
}
type NullableInput[T any] struct{ input Input[T] }

func Set[T any](v T) Input[T]                     { return Input[T]{assigned, valueNode{v}} }
func SetExpr[T any](v Expr[T]) Input[T]           { return Input[T]{assigned, v.node} }
func Default[T any]() Input[T]                    { return Input[T]{kind: defaulted} }
func Nullable[T any](v Input[T]) NullableInput[T] { return NullableInput[T]{v} }
func Null[T any]() NullableInput[T]               { return NullableInput[T]{Input[T]{assigned, literalNode("NULL")}} }

type Assignment struct {
	ref   Ref
	name  string
	kind  inputKind
	value node
}

func (c Column[T]) Set(v T) Assignment                           { return c.Assign(Set(v)) }
func (c Column[T]) SetExpr(v Expr[T]) Assignment                 { return c.Assign(SetExpr(v)) }
func (c Column[T]) Assign(v Input[T]) Assignment                 { return Assignment{c.ref, c.name, v.kind, v.value} }
func (c Column[T]) AssignNullable(v NullableInput[T]) Assignment { return c.Assign(v.input) }

type mutationKind uint8

const (
	insertKind mutationKind = iota
	updateKind
	deleteKind
)

// Mutation shares predicates with SELECT. Updates/deletes require a predicate;
// AllRows makes a deliberately unbounded write explicit.
type Mutation struct {
	kind      mutationKind
	ref       Ref
	sets      []Assignment
	where     []Predicate
	returning []Selection
	allRows   bool
	conflict  *conflict
}

func Insert(ref Ref, sets ...Assignment) Mutation {
	return Mutation{kind: insertKind, ref: ref, sets: slices.Clone(sets)}
}
func Update(ref Ref, sets ...Assignment) Mutation {
	return Mutation{kind: updateKind, ref: ref, sets: slices.Clone(sets)}
}
func Delete(ref Ref) Mutation { return Mutation{kind: deleteKind, ref: ref} }
func (m Mutation) Where(ps ...Predicate) Mutation {
	m.where = append(slices.Clone(m.where), ps...)
	return m
}
func (m Mutation) AllRows() Mutation                  { m.allRows = true; return m }
func (m Mutation) Returning(cs ...Selection) Mutation { m.returning = slices.Clone(cs); return m }

func (m Mutation) Build(d Dialect) (Statement, error) {
	if d > MySQL {
		return Statement{}, fmt.Errorf("unsupported dialect")
	}
	if m.ref.b == nil || m.ref.b.table == nil || m.ref.b.alias == "" {
		return Statement{}, fmt.Errorf("mutation requires a table reference and alias")
	}
	t := m.ref.b.table
	if t.info.ReadOnly {
		return Statement{}, fmt.Errorf("table is read-only")
	}
	if t.err != nil {
		return Statement{}, t.err
	}
	if m.kind != insertKind && len(m.where) == 0 && !m.allRows {
		return Statement{}, fmt.Errorf("update/delete requires Where or AllRows")
	}
	if m.kind == insertKind && len(m.where) > 0 {
		return Statement{}, fmt.Errorf("insert does not accept Where")
	}
	if len(m.returning) > 0 && d == MySQL {
		return Statement{}, fmt.Errorf("MySQL does not support this RETURNING operation")
	}
	if m.conflict != nil && m.kind != insertKind {
		return Statement{}, fmt.Errorf("ON CONFLICT is only valid for inserts")
	}
	r := &renderer{dialect: d, scope: map[*binding]string{m.ref.b: identifierAlias(m.ref.b.alias, d)}}
	sets := make([]Assignment, 0, len(m.sets))
	seen := map[string]bool{}
	for _, a := range m.sets {
		if a.kind == omitted {
			continue
		}
		if a.ref.b != m.ref.b {
			return Statement{}, fmt.Errorf("assignment belongs to another table reference")
		}
		c, ok := t.column(a.name)
		if !ok || c.Generated || seen[a.name] {
			return Statement{}, fmt.Errorf("unknown, generated, or repeated assignment %s", a.name)
		}
		if a.kind == defaulted && !c.HasDefault && !c.Nullable {
			return Statement{}, fmt.Errorf("column %s has no default", a.name)
		}
		if n, ok := a.value.(literalNode); ok && n == "NULL" && !c.Nullable {
			return Statement{}, fmt.Errorf("column %s is not nullable", a.name)
		}
		if a.kind == defaulted && d == SQLite && m.kind == updateKind {
			return Statement{}, fmt.Errorf("SQLite does not support SET column = DEFAULT")
		}
		seen[a.name] = true
		// Omitting a DEFAULT column in INSERT works in every supported dialect.
		if a.kind == defaulted && m.kind == insertKind {
			continue
		}
		sets = append(sets, a)
	}
	if m.kind == insertKind {
		for _, c := range t.info.Columns {
			if !c.Nullable && !c.Generated && !c.HasDefault && !seen[c.Name] {
				return Statement{}, fmt.Errorf("required insert column %s is missing", c.Name)
			}
		}
		r.WriteString("INSERT INTO ")
		writeTableName(r, t)
		// INSERT values cannot reference the row being inserted.
		r.scope = map[*binding]string{}
		if len(sets) == 0 {
			if d == MySQL {
				r.WriteString(" () VALUES ()")
			} else {
				r.WriteString(" DEFAULT VALUES")
			}
		} else {
			r.WriteString(" (")
			for i, a := range sets {
				if i > 0 {
					r.WriteString(", ")
				}
				r.ident(a.name)
			}
			r.WriteString(") VALUES (")
			for i, a := range sets {
				if i > 0 {
					r.WriteString(", ")
				}
				r.expr(a.value)
			}
			r.WriteByte(')')
		}
	} else {
		if m.kind == updateKind {
			if len(sets) == 0 {
				return Statement{}, fmt.Errorf("update has no assignments")
			}
			r.WriteString("UPDATE ")
			r.ref(m.ref)
			r.WriteString(" SET ")
			for i, a := range sets {
				if i > 0 {
					r.WriteString(", ")
				}
				r.ident(a.name)
				r.WriteString(" = ")
				if a.kind == defaulted {
					r.WriteString("DEFAULT")
				} else {
					r.expr(a.value)
				}
			}
		} else {
			r.WriteString("DELETE FROM ")
			r.ref(m.ref)
		}
		writeWhere(r, " WHERE ", m.where)
	}
	if m.conflict != nil {
		m.writeConflict(r)
	}
	cols := make([]string, len(m.returning))
	if len(m.returning) > 0 {
		r.WriteString(" RETURNING ")
		for i, c := range m.returning {
			if i > 0 {
				r.WriteString(", ")
			}
			// Portable RETURNING uses unqualified columns, not the mutation alias.
			if n, ok := c.node.(columnNode); ok && n.ref.b == m.ref.b {
				if _, exists := t.column(n.name); !exists {
					r.fail(fmt.Errorf("unknown returning column %s", n.name))
				}
				r.ident(n.name)
				cols[i] = n.name
			} else {
				r.fail(fmt.Errorf("RETURNING requires target-table columns"))
			}
			if c.label != "" {
				r.WriteString(" AS ")
				r.ident(identifierAlias(c.label, d))
				cols[i] = identifierAlias(c.label, d)
			}
		}
	}
	if r.err != nil {
		return Statement{}, r.err
	}
	return Statement{r.String(), r.args, cols}, nil
}

func writeTableName(r *renderer, t *Table) {
	if t.info.Schema != "" {
		r.ident(t.info.Schema)
		r.WriteByte('.')
	}
	r.ident(t.info.Name)
}
