package next

import (
	"fmt"
	"slices"
)

// RowsInsert writes a batch using shared schema metadata and typed assignments.
// Callers bound the number of rows to their database's parameter limit.
type RowsInsert struct {
	ref  Ref
	rows [][]Assignment
}

func InsertRows(ref Ref, rows ...[]Assignment) RowsInsert {
	copyRows := make([][]Assignment, len(rows))
	for i, row := range rows {
		copyRows[i] = slices.Clone(row)
	}
	return RowsInsert{ref, copyRows}
}

func (q RowsInsert) Build(d Dialect) (Statement, error) {
	if len(q.rows) == 0 {
		return Statement{}, fmt.Errorf("bulk insert requires rows")
	}
	// The single-row builder validates table, dialect, required fields, and bindings.
	values := make([]map[string]Assignment, len(q.rows))
	used := map[string]bool{}
	for i, row := range q.rows {
		mutation := Insert(q.ref, row...)
		if _, err := mutation.Build(d); err != nil {
			return Statement{}, fmt.Errorf("bulk insert row %d: %w", i, err)
		}
		sets, _, err := mutation.assignments(d, q.ref.Table())
		if err != nil {
			return Statement{}, err
		}
		values[i] = map[string]Assignment{}
		for _, set := range sets {
			values[i][set.name] = set
			used[set.name] = true
		}
	}
	t := q.ref.Table()
	columns := make([]string, 0, len(used))
	for _, c := range t.info.Columns {
		if used[c.Name] {
			columns = append(columns, c.Name)
		}
	}
	if len(columns) == 0 {
		if len(q.rows) == 1 {
			return Insert(q.ref, q.rows[0]...).Build(d)
		}
		return Statement{}, fmt.Errorf("bulk insert requires at least one assigned column")
	}
	r := &renderer{dialect: d, scope: map[*binding]string{}}
	r.WriteString("INSERT INTO ")
	writeTableName(r, t)
	r.WriteString(" (")
	for i, name := range columns {
		if i > 0 {
			r.WriteString(", ")
		}
		r.ident(name)
	}
	r.WriteString(") VALUES ")
	for i, row := range values {
		if i > 0 {
			r.WriteString(", ")
		}
		r.WriteByte('(')
		for j, name := range columns {
			if j > 0 {
				r.WriteString(", ")
			}
			if assignment, ok := row[name]; ok {
				r.expr(assignment.value)
			} else {
				if d == SQLite {
					return Statement{}, fmt.Errorf("SQLite does not support DEFAULT inside bulk VALUES")
				}
				r.WriteString("DEFAULT")
			}
		}
		r.WriteByte(')')
	}
	if r.err != nil {
		return Statement{}, r.err
	}
	return Statement{SQL: r.String(), Args: r.args}, nil
}

var _ Buildable = RowsInsert{}
