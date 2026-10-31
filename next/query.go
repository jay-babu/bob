package next

import (
	"context"
	"fmt"
)

// RowQuery keeps the full-row decoder attached through query composition.
// SQL construction and cursor management remain in the shared runtime.
// Its projection cannot be replaced by application query modifiers.
type RowQuery[T any] struct {
	query      SelectQuery
	projection Projection[T]
	relations  *RelationPlan
}

// FullRows is used by generated table bindings; callers normally use c.Query().
func FullRows[T any](from Ref, projection Projection[T]) RowQuery[T] {
	return RowQuery[T]{query: Select(from, projection.Columns...), projection: projection}
}

func (q RowQuery[T]) WithRelations(plan RelationPlan) RowQuery[T] {
	if plan.BatchSize == 0 {
		plan.BatchSize = 100
	}
	if plan.MaxRows == 0 {
		plan.MaxRows = 10000
	}
	if plan.Owner.Table != q.query.from.Table() || plan.BatchSize < 1 || plan.MaxRows < 1 {
		q.query.err = fmt.Errorf("invalid relationship plan owner or bounds")
	} else if err := validateSteps(plan.Owner, plan.Steps, 0); err != nil {
		q.query.err = err
	} else {
		plan.Steps = cloneSteps(plan.Steps)
	}
	q.relations = &plan
	return q
}

func (q RowQuery[T]) Build(d Dialect) (Statement, error) { return q.query.Build(d) }
func (q RowQuery[T]) Where(ps ...Predicate) RowQuery[T] {
	q.query = q.query.Where(ps...)
	return q
}
func (q RowQuery[T]) OrderBy(os ...Order) RowQuery[T] {
	q.query = q.query.OrderBy(os...)
	return q
}
func (q RowQuery[T]) Limit(n int) RowQuery[T]  { q.query = q.query.Limit(n); return q }
func (q RowQuery[T]) Offset(n int) RowQuery[T] { q.query = q.query.Offset(n); return q }
func (q RowQuery[T]) Distinct() RowQuery[T]    { q.query = q.query.Distinct(); return q }
func (q RowQuery[T]) SeekAfter(keys ...CursorKey) RowQuery[T] {
	q.query = q.query.SeekAfter(keys...)
	return q
}
func (q RowQuery[T]) JoinRelation(rel Relation, from, to Ref, left bool) RowQuery[T] {
	q.query = q.query.JoinRelation(rel, from, to, left)
	return q
}
func (q RowQuery[T]) JoinOn(to Ref, on Predicate, left bool) RowQuery[T] {
	q.query = q.query.JoinOn(to, on, left)
	return q
}

func (q RowQuery[T]) AllContext(ctx context.Context) ([]T, error) {
	s, err := sessionFromContext(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := All(ctx, s, q.query, q.projection)
	if err != nil {
		return nil, err
	}
	if err := q.load(ctx, s, rows); err != nil {
		return nil, err
	}
	return rows, nil
}
func (q RowQuery[T]) OneContext(ctx context.Context) (T, error) {
	s, err := sessionFromContext(ctx)
	if err != nil {
		return *new(T), err
	}
	row, err := One(ctx, s, q.query, q.projection)
	if err == nil {
		err = q.load(ctx, s, []T{row})
	}
	if err != nil {
		return *new(T), err
	}
	return row, nil
}
func (q RowQuery[T]) EachContext(ctx context.Context, yield func(T) error) error {
	if q.relations != nil {
		return fmt.Errorf("streaming relationship loads require an explicit batched loader")
	}
	s, err := sessionFromContext(ctx)
	if err != nil {
		return err
	}
	return Each(ctx, s, q.query, q.projection, yield)
}

func (q RowQuery[T]) load(ctx context.Context, s Session, rows []T) error {
	if q.relations == nil {
		return nil
	}
	parents := make([]any, len(rows))
	for i, row := range rows {
		parents[i] = row
	}
	plan := q.relations
	total := 0
	return s.loadRelations(ctx, plan.Owner, parents, plan.Steps, plan.BatchSize, plan.MaxRows, &total)
}
func (q RowQuery[T]) CountContext(ctx context.Context) (int64, error) {
	s, err := sessionFromContext(ctx)
	if err != nil {
		return 0, err
	}
	return One(ctx, s, ParentCount(q.query), CountProjection())
}
