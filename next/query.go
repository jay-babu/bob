package next

import "context"

// RowQuery keeps the full-row decoder attached through query composition.
// SQL construction and cursor management remain in the shared runtime.
// Its projection cannot be replaced by application query modifiers.
type RowQuery[T any] struct {
	query      SelectQuery
	projection Projection[T]
}

// FullRows is used by generated table bindings; callers normally use c.Query().
func FullRows[T any](from Ref, projection Projection[T]) RowQuery[T] {
	return RowQuery[T]{Select(from, projection.Columns...), projection}
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
	return All(ctx, s, q.query, q.projection)
}
func (q RowQuery[T]) OneContext(ctx context.Context) (T, error) {
	s, err := sessionFromContext(ctx)
	if err != nil {
		return *new(T), err
	}
	return One(ctx, s, q.query, q.projection)
}
func (q RowQuery[T]) EachContext(ctx context.Context, yield func(T) error) error {
	s, err := sessionFromContext(ctx)
	if err != nil {
		return err
	}
	return Each(ctx, s, q.query, q.projection, yield)
}
func (q RowQuery[T]) CountContext(ctx context.Context) (int64, error) {
	s, err := sessionFromContext(ctx)
	if err != nil {
		return 0, err
	}
	return One(ctx, s, ParentCount(q.query), CountProjection())
}
