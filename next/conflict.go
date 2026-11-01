package next

import (
	"fmt"
	"slices"
)

type conflict struct {
	columns []Selection
	sets    []Assignment
	where   []Predicate
	update  bool
}

// OnConflict defaults to DO NOTHING. DO UPDATE requires a known unique key.
// PostgreSQL and SQLite are supported; MySQL's different semantics are explicit
// SQL through SQL(), rather than a misleading cross-dialect emulation.
func (m Mutation) OnConflict(columns ...Selection) Mutation {
	m.conflict = &conflict{columns: slices.Clone(columns)}
	return m
}

func (m Mutation) DoUpdate(sets ...Assignment) Mutation {
	c := conflict{}
	if m.conflict != nil {
		c = *m.conflict
	}
	c.update = true
	c.sets = slices.Clone(sets)
	m.conflict = &c
	return m
}

func (m Mutation) ConflictWhere(ps ...Predicate) Mutation {
	c := conflict{}
	if m.conflict != nil {
		c = *m.conflict
	}
	c.where = append(slices.Clone(c.where), ps...)
	m.conflict = &c
	return m
}

// Excluded is the proposed insert value and is valid only in conflict updates.
func Excluded[T any](column Column[T]) Expr[T] { return Expr[T]{excludedNode(column)} }

type excludedNode struct {
	ref  Ref
	name string
}

func (n excludedNode) write(r *renderer) {
	if r.excluded == nil || n.ref.Table() != r.excluded {
		r.fail(fmt.Errorf("excluded column used outside its conflict update"))
		return
	}
	if _, ok := r.excluded.column(n.name); !ok {
		r.fail(fmt.Errorf("unknown excluded column"))
		return
	}
	r.ident("excluded")
	r.WriteByte('.')
	r.ident(n.name)
}

func (m Mutation) writeConflict(r *renderer) {
	if r.dialect == MySQL {
		r.fail(fmt.Errorf("ON CONFLICT is unsupported for MySQL"))
		return
	}
	c := m.conflict
	t := m.ref.Table()
	columns := make([]string, len(c.columns))
	seen := map[string]bool{}
	for i, sel := range c.columns {
		n, ok := sel.node.(columnNode)
		if !ok || n.ref.b != m.ref.b || seen[n.name] {
			r.fail(fmt.Errorf("conflict target requires distinct target-table columns"))
			return
		}
		if _, ok := t.column(n.name); !ok {
			r.fail(fmt.Errorf("unknown conflict column"))
			return
		}
		seen[n.name] = true
		columns[i] = n.name
	}
	if len(columns) > 0 && !t.unique(columns) {
		r.fail(fmt.Errorf("conflict target is not a known unique key"))
		return
	}
	r.WriteString(" ON CONFLICT")
	if len(columns) > 0 {
		r.WriteString(" (")
		for i, col := range columns {
			if i > 0 {
				r.WriteString(", ")
			}
			r.ident(col)
		}
		r.WriteByte(')')
	}
	if !c.update {
		if len(c.where) > 0 {
			r.fail(fmt.Errorf("DO NOTHING does not accept a conflict predicate"))
			return
		}
		r.WriteString(" DO NOTHING")
		return
	}
	if len(columns) == 0 {
		r.fail(fmt.Errorf("DO UPDATE requires a conflict target"))
		return
	}
	r.WriteString(" DO UPDATE SET ")
	count := 0
	seen = map[string]bool{}
	// INSERT has no target alias; conflict expressions use the actual table name.
	r.scope = map[*binding]string{m.ref.b: t.info.Name}
	r.excluded = t
	defer func() { r.excluded = nil }()
	for _, a := range c.sets {
		if a.kind == omitted {
			continue
		}
		info, ok := t.column(a.name)
		if a.ref.b != m.ref.b || !ok || info.Generated || seen[a.name] || a.kind == defaulted {
			r.fail(fmt.Errorf("invalid conflict assignment %s", a.name))
			return
		}
		if value, ok := a.value.(literalNode); ok && value == "NULL" && !info.Nullable {
			r.fail(fmt.Errorf("conflict assignment is NULL for nonnullable column"))
			return
		}
		seen[a.name] = true
		if count > 0 {
			r.WriteString(", ")
		}
		count++
		r.ident(a.name)
		r.WriteString(" = ")
		r.expr(a.value)
	}
	if count == 0 {
		r.fail(fmt.Errorf("DO UPDATE has no assignments"))
		return
	}
	writeWhere(r, " WHERE ", c.where)
}
