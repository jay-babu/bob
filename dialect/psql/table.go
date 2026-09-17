package psql

import (
	"context"
	"reflect"

	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/dialect/psql/dialect"
	"github.com/stephenafamo/bob/dialect/psql/dm"
	"github.com/stephenafamo/bob/dialect/psql/im"
	"github.com/stephenafamo/bob/dialect/psql/mm"
	"github.com/stephenafamo/bob/dialect/psql/um"
	"github.com/stephenafamo/bob/expr"
	"github.com/stephenafamo/bob/internal"
	"github.com/stephenafamo/bob/internal/mappings"
	"github.com/stephenafamo/bob/orm"
	"github.com/stephenafamo/scan"
)

type (
	setter[T any]                      = orm.Setter[T, *dialect.InsertQuery, *dialect.UpdateQuery]
	ormInsertQuery[T any, Tslice ~[]T] = orm.ModelQuery[*dialect.InsertQuery, T, Tslice]
	ormUpdateQuery[T any, Tslice ~[]T] = orm.ModelQuery[*dialect.UpdateQuery, T, Tslice]
	ormDeleteQuery[T any, Tslice ~[]T] = orm.ModelQuery[*dialect.DeleteQuery, T, Tslice]
	ormMergeQuery[T any, Tslice ~[]T]  = orm.ModelQuery[*dialect.MergeQuery, T, Tslice]
)

func NewTable[T any, Tset setter[T], C bob.Expression](schema, tableName string, columns C) *Table[T, []T, Tset, C] {
	return newTableWithMapper[T, []T, Tset](schema, tableName, columns, scan.StructMapper[T]())
}

// NewTablex creates a new Table with a required custom scanner.
func NewTablex[T any, Tslice ~[]T, Tset setter[T], C bob.Expression](schema, table string, columns C, scanner scan.Mapper[T]) *Table[T, Tslice, Tset, C] {
	if scanner == nil {
		panic("psql: nil mapper passed to NewTablex")
	}

	return newTableWithMapper[T, Tslice, Tset](schema, table, columns, scanner)
}

func newTableWithMapper[T any, Tslice ~[]T, Tset setter[T], C bob.Expression](schema, table string, columns C, scanner scan.Mapper[T]) *Table[T, Tslice, Tset, C] {
	setMapping := mappings.GetMappings(reflect.TypeOf((*new(Tset))))
	view, mappings := newViewWithMapper[T, Tslice](schema, table, columns, scanner)
	t := &Table[T, Tslice, Tset, C]{
		View:             view,
		pkCols:           expr.NewColumnsExpr(mappings.PKs...).WithParent(view.alias),
		setterMapping:    setMapping,
		nonGeneratedCols: internal.FilterNonZero(mappings.NonGenerated),
	}

	return t
}

// The table contains extract information from the struct and contains
// caches ???
type Table[T any, Tslice ~[]T, Tset setter[T], C bob.Expression] struct {
	*View[T, Tslice, C]
	pkCols           expr.ColumnsExpr
	setterMapping    mappings.Mapping
	nonGeneratedCols []string

	BeforeInsertHooks bob.Hooks[Tset, bob.SkipModelHooksKey]
	AfterInsertHooks  bob.Hooks[Tslice, bob.SkipModelHooksKey]

	BeforeUpdateHooks bob.Hooks[Tslice, bob.SkipModelHooksKey]
	AfterUpdateHooks  bob.Hooks[Tslice, bob.SkipModelHooksKey]

	BeforeDeleteHooks bob.Hooks[Tslice, bob.SkipModelHooksKey]
	AfterDeleteHooks  bob.Hooks[Tslice, bob.SkipModelHooksKey]

	BeforeMergeHooks bob.Hooks[Tslice, bob.SkipModelHooksKey]
	AfterMergeHooks  bob.Hooks[Tslice, bob.SkipModelHooksKey]

	InsertQueryHooks bob.Hooks[*dialect.InsertQuery, bob.SkipQueryHooksKey]
	UpdateQueryHooks bob.Hooks[*dialect.UpdateQuery, bob.SkipQueryHooksKey]
	DeleteQueryHooks bob.Hooks[*dialect.DeleteQuery, bob.SkipQueryHooksKey]
	MergeQueryHooks  bob.Hooks[*dialect.MergeQuery, bob.SkipQueryHooksKey]
}

// Returns the primary key columns for this table.
func (t *Table[T, Tslice, Tset, C]) PrimaryKey() expr.ColumnsExpr {
	return t.pkCols
}

// Starts an insert query for this table
func (t *Table[T, Tslice, Tset, C]) Insert(queryMods ...bob.Mod[*dialect.InsertQuery]) *ormInsertQuery[T, Tslice] {
	q := &ormInsertQuery[T, Tslice]{
		BaseQuery: Insert(im.Into(t.NameAsExpr(), t.nonGeneratedCols...)),
		Hooks:     &t.InsertQueryHooks,
		Scanner:   t.scanner,
	}

	q.Expression.AppendContextualModFunc(
		func(ctx context.Context, q *dialect.InsertQuery) (context.Context, error) {
			if !q.HasReturning() {
				q.AppendReturning(t.Columns)
			}
			return ctx, nil
		},
	)

	q.Apply(queryMods...)

	return q
}

// Starts an Update query for this table
func (t *Table[T, Tslice, Tset, C]) Update(queryMods ...bob.Mod[*dialect.UpdateQuery]) *ormUpdateQuery[T, Tslice] {
	q := &ormUpdateQuery[T, Tslice]{
		BaseQuery: Update(um.Table(t.NameAsExpr())),
		Hooks:     &t.UpdateQueryHooks,
		Scanner:   t.scanner,
	}

	q.Expression.AppendContextualModFunc(
		func(ctx context.Context, q *dialect.UpdateQuery) (context.Context, error) {
			if !q.HasReturning() {
				q.AppendReturning(t.Columns)
			}
			return ctx, nil
		},
	)

	q.Apply(queryMods...)

	return q
}

// Starts a Delete query for this table
func (t *Table[T, Tslice, Tset, C]) Delete(queryMods ...bob.Mod[*dialect.DeleteQuery]) *ormDeleteQuery[T, Tslice] {
	q := &ormDeleteQuery[T, Tslice]{
		BaseQuery: Delete(dm.From(t.NameAsExpr())),
		Hooks:     &t.DeleteQueryHooks,
		Scanner:   t.scanner,
	}

	q.Expression.AppendContextualModFunc(
		func(ctx context.Context, q *dialect.DeleteQuery) (context.Context, error) {
			if !q.HasReturning() {
				q.AppendReturning(t.Columns)
			}
			return ctx, nil
		},
	)

	q.Apply(queryMods...)

	return q
}

// Starts a Merge query for this table
// The caller must provide USING and WHEN clauses via queryMods
// RETURNING clause is automatically added if version >= 17 is set in context.
// Use psql.SetVersion(ctx, 17) to enable automatic RETURNING for MERGE.
// For older versions, use mm.Returning() explicitly if needed.
func (t *Table[T, Tslice, Tset, C]) Merge(queryMods ...bob.Mod[*dialect.MergeQuery]) *ormMergeQuery[T, Tslice] {
	q := &ormMergeQuery[T, Tslice]{
		BaseQuery: Merge(mm.Into(t.NameAsExpr())),
		Hooks:     &t.MergeQueryHooks,
		Scanner:   t.scanner,
	}

	q.Expression.AppendContextualModFunc(
		func(ctx context.Context, q *dialect.MergeQuery) (context.Context, error) {
			// RETURNING in MERGE requires version 17+
			if VersionAtLeast(ctx, 17) && !q.HasReturning() {
				q.AppendReturning(t.Columns)
			}
			return ctx, nil
		},
	)

	q.Apply(queryMods...)

	return q
}
