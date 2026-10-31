package next

import (
	"fmt"
	"maps"
	"slices"
)

// Statement is executable SQL. Args are never interpolated into SQL.
type Statement struct {
	SQL     string
	Args    []any
	Columns []string
}
type Buildable interface {
	Build(Dialect) (Statement, error)
}

type join struct {
	ref        Ref
	on         Predicate
	left, many bool
}

// SelectQuery is immutable: every operation returns an independent query value.
type SelectQuery struct {
	from          Ref
	columns       []Selection
	where, having []Predicate
	joins         []join
	group         []node
	order         []Order
	limit, offset *int
	err           error
	distinct      bool
}

func Select(from Ref, columns ...Selection) SelectQuery {
	return SelectQuery{from: from, columns: slices.Clone(columns)}
}
func (q SelectQuery) Where(ps ...Predicate) SelectQuery {
	q.where = append(slices.Clone(q.where), ps...)
	return q
}
func (q SelectQuery) Having(ps ...Predicate) SelectQuery {
	q.having = append(slices.Clone(q.having), ps...)
	return q
}
func (q SelectQuery) OrderBy(os ...Order) SelectQuery {
	q.order = append(slices.Clone(q.order), os...)
	return q
}
func (q SelectQuery) GroupBy(cs ...Selection) SelectQuery {
	q.group = slices.Clone(q.group)
	for _, c := range cs {
		q.group = append(q.group, c.node)
	}
	return q
}
func (q SelectQuery) Limit(n int) SelectQuery  { q.limit = &n; return q }
func (q SelectQuery) Offset(n int) SelectQuery { q.offset = &n; return q }
func (q SelectQuery) Distinct() SelectQuery    { q.distinct = true; return q }

// JoinOn permits arbitrary SQL joins. Their multiplicity is unknown, so parent
// pagination is rejected. Use relationship joins or Exists when paging parents.
func (q SelectQuery) JoinOn(to Ref, on Predicate, left bool) SelectQuery {
	q.joins = append(slices.Clone(q.joins), join{to, on, left, true})
	return q
}

func (q SelectQuery) JoinRelation(rel Relation, from, to Ref, left bool) SelectQuery {
	p, err := rel.predicate(from, to)
	if err != nil {
		q.err = err
		return q
	}
	q.joins = append(slices.Clone(q.joins), join{to, p, left, !rel.To.unique(rel.ToColumns)})
	return q
}

func (q SelectQuery) Build(d Dialect) (Statement, error) {
	r := &renderer{dialect: d, scope: map[*binding]string{}}
	if d > MySQL {
		return Statement{}, fmt.Errorf("unsupported dialect")
	}
	q.write(r)
	if r.err != nil {
		return Statement{}, r.err
	}
	cols := make([]string, len(q.columns))
	for i, c := range q.columns {
		cols[i] = c.label
		if cols[i] == "" {
			if n, ok := c.node.(columnNode); ok {
				cols[i] = n.name
			}
		}
	}
	return Statement{r.String(), r.args, cols}, nil
}

func (q SelectQuery) write(r *renderer) {
	if q.err != nil {
		r.fail(q.err)
		return
	}
	if len(q.columns) == 0 {
		r.fail(fmt.Errorf("select requires an explicit projection"))
		return
	}
	if (q.limit != nil && *q.limit < 0) || (q.offset != nil && *q.offset < 0) {
		r.fail(fmt.Errorf("negative pagination"))
		return
	}
	if q.offset != nil && q.limit == nil {
		r.fail(fmt.Errorf("offset requires a limit"))
		return
	}
	for _, j := range q.joins {
		if j.many && (q.limit != nil || q.offset != nil) {
			r.fail(fmt.Errorf("pagination over a multiplying join: filter with Exists, then batch-load children"))
			return
		}
	}
	outer := r.scope
	r.scope = maps.Clone(outer)
	defer func() { r.scope = outer }()
	add := func(ref Ref) {
		if ref.b == nil || ref.b.table == nil || ref.b.alias == "" {
			r.fail(fmt.Errorf("table reference requires a table and alias"))
			return
		}
		for b, alias := range r.scope {
			if b == ref.b || alias == ref.b.alias {
				r.fail(fmt.Errorf("duplicate or shadowed table alias %s", alias))
				return
			}
		}
		r.scope[ref.b] = ref.b.alias
	}
	add(q.from)
	for _, j := range q.joins {
		add(j.ref)
	}
	r.WriteString("SELECT ")
	if q.distinct {
		r.WriteString("DISTINCT ")
	}
	for i, c := range q.columns {
		if i > 0 {
			r.WriteString(", ")
		}
		r.expr(c.node)
		if c.label != "" {
			r.WriteString(" AS ")
			r.ident(c.label)
		}
	}
	r.WriteString(" FROM ")
	r.ref(q.from)
	// ON may refer only to the left side and this join, never a later join.
	full := r.scope
	r.scope = maps.Clone(outer)
	if q.from.b != nil {
		r.scope[q.from.b] = q.from.b.alias
	}
	for _, j := range q.joins {
		if j.left {
			r.WriteString(" LEFT JOIN ")
		} else {
			r.WriteString(" INNER JOIN ")
		}
		r.ref(j.ref)
		if j.ref.b != nil {
			r.scope[j.ref.b] = j.ref.b.alias
		}
		r.WriteString(" ON ")
		r.expr(j.on.node)
	}
	r.scope = full
	writeWhere(r, " WHERE ", q.where)
	if len(q.group) > 0 {
		r.WriteString(" GROUP BY ")
		for i, n := range q.group {
			if i > 0 {
				r.WriteString(", ")
			}
			r.expr(n)
		}
	}
	writeWhere(r, " HAVING ", q.having)
	if len(q.order) > 0 {
		r.WriteString(" ORDER BY ")
		for i, o := range q.order {
			if i > 0 {
				r.WriteString(", ")
			}
			r.expr(o.node)
			if o.desc {
				r.WriteString(" DESC")
			} else {
				r.WriteString(" ASC")
			}
		}
	}
	if q.limit != nil {
		r.WriteString(" LIMIT ")
		r.arg(*q.limit)
	}
	if q.offset != nil {
		r.WriteString(" OFFSET ")
		r.arg(*q.offset)
	}
}

func writeWhere(r *renderer, prefix string, ps []Predicate) {
	if len(ps) == 0 {
		return
	}
	r.WriteString(prefix)
	r.expr(And(ps...).node)
}
