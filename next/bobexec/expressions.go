package bobexec

import (
	"context"
	"fmt"
	"io"
	"slices"

	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/dialect/psql"
	"github.com/stephenafamo/bob/dialect/psql/dialect"
	"github.com/stephenafamo/bob/dialect/psql/sm"
	"github.com/stephenafamo/bob/next"
)

// Column uses generated column identity in PostgreSQL-specific SQL builders.
// Aliases belong to the surrounding builder; native scope rewriting is not used.
func Column[T any](column next.Column[T]) psql.Expression {
	return psql.Quote(column.Reference().Alias(), column.Name())
}

func Table(table *next.Table) psql.Expression {
	if table == nil {
		return dialect.NewExpression(bob.ExpressionFunc(func(context.Context, io.StringWriter, bob.Dialect, int) ([]any, error) {
			return nil, fmt.Errorf("table metadata is required")
		}))
	}
	info := table.Info()
	if info.Schema == "" {
		return psql.Quote(info.Name)
	}
	return psql.Quote(info.Schema, info.Name)
}

// LeftJoin retains every column of a composite relationship, including tenant
// keys. It needs no generated per-table join family or row relationship graph.
func LeftJoin(relation next.Relation, fromAlias, toAlias string) bob.Mod[*dialect.SelectQuery] {
	checks := make([]bob.Expression, 0, len(relation.FromColumns))
	fromColumns, toColumns := slices.Clone(relation.FromColumns), slices.Clone(relation.ToColumns)
	joinedTable := bob.ExpressionFunc(func(ctx context.Context, w io.StringWriter, d bob.Dialect, start int) ([]any, error) {
		if relation.From == nil || relation.To == nil || fromAlias == "" || toAlias == "" || len(fromColumns) == 0 || len(fromColumns) != len(toColumns) {
			return nil, fmt.Errorf("incomplete relationship join")
		}
		from, to := relation.From.Info(), relation.To.Info()
		for i, name := range fromColumns {
			if !slices.ContainsFunc(from.Columns, func(c next.ColumnInfo) bool { return c.Name == name }) || !slices.ContainsFunc(to.Columns, func(c next.ColumnInfo) bool { return c.Name == toColumns[i] }) {
				return nil, fmt.Errorf("unknown relationship join column")
			}
		}
		return Table(relation.To).WriteSQL(ctx, w, d, start)
	})
	for i, name := range fromColumns {
		if i >= len(toColumns) {
			break
		}
		checks = append(checks, psql.Quote(fromAlias, name).EQ(psql.Quote(toAlias, toColumns[i])))
	}
	return sm.LeftJoin(joinedTable).As(toAlias).On(checks...)
}
