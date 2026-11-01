package next

import "fmt"

// ParentCount counts distinct primary keys matching FROM/JOIN/WHERE before
// pagination. It rejects grouped queries, whose notion of a parent is ambiguous.
func ParentCount(query SelectQuery) Buildable { return parentCount{query} }

type parentCount struct{ query SelectQuery }

func (c parentCount) Build(d Dialect) (Statement, error) {
	q := c.query
	if q.from.Table() == nil || len(q.from.Table().info.PrimaryKey) == 0 {
		return Statement{}, fmt.Errorf("parent count requires a primary key")
	}
	if len(q.group) > 0 || len(q.having) > 0 {
		return Statement{}, fmt.Errorf("parent count does not accept grouped queries")
	}
	if d > MySQL {
		return Statement{}, fmt.Errorf("unsupported dialect")
	}
	q.columns = nil
	for _, name := range q.from.Table().info.PrimaryKey {
		q.columns = append(q.columns, Selection{columnNode{q.from, name}, ""})
	}
	q.distinct = true
	q.limit = nil
	q.offset = nil
	q.order = nil
	r := &renderer{dialect: d, scope: map[*binding]string{}}
	r.WriteString("SELECT COUNT(*) AS ")
	r.ident("count")
	r.WriteString(" FROM (")
	q.write(r)
	r.WriteString(") AS ")
	r.ident("parent_count")
	if r.err != nil {
		return Statement{}, r.err
	}
	return Statement{r.String(), r.args, []string{"count"}}, nil
}

func CountProjection() Projection[int64] {
	return Projection[int64]{Columns: []Selection{Count().Select("count")}, Scan: func(s Scanner) (int64, error) { var n int64; err := s.Scan(&n); return n, err }}
}
