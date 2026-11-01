package next

import "context"

// RowMutation retains a generated full-row decoder for RETURNING operations.
// ExecContext and ExecOneContext execute without selecting returned columns.
type RowMutation[T any] struct {
	mutation   Mutation
	projection Projection[T]
}

func FullWrite[T any](mutation Mutation, projection Projection[T]) RowMutation[T] {
	return RowMutation[T]{mutation, projection}
}

func (q RowMutation[T]) Build(d Dialect) (Statement, error) { return q.mutation.Build(d) }
func (q RowMutation[T]) Where(ps ...Predicate) RowMutation[T] {
	q.mutation = q.mutation.Where(ps...)
	return q
}

func (q RowMutation[T]) AllRows() RowMutation[T] {
	q.mutation = q.mutation.AllRows()
	return q
}

func (q RowMutation[T]) OnConflict(columns ...Selection) RowMutation[T] {
	q.mutation = q.mutation.OnConflict(columns...)
	return q
}

func (q RowMutation[T]) DoUpdate(sets ...Assignment) RowMutation[T] {
	q.mutation = q.mutation.DoUpdate(sets...)
	return q
}

func (q RowMutation[T]) ConflictWhere(ps ...Predicate) RowMutation[T] {
	q.mutation = q.mutation.ConflictWhere(ps...)
	return q
}

func (q RowMutation[T]) ExecContext(ctx context.Context) (int64, error) {
	return ExecContext(ctx, q.mutation)
}

func (q RowMutation[T]) ExecOneContext(ctx context.Context) error {
	return ExecOneContext(ctx, q.mutation)
}

func (q RowMutation[T]) AllContext(ctx context.Context) ([]T, error) {
	s, err := sessionFromContext(ctx)
	if err != nil {
		return nil, err
	}
	return All(ctx, s, q.mutation.Returning(q.projection.Columns...), q.projection)
}

func (q RowMutation[T]) OneContext(ctx context.Context) (T, error) {
	s, err := sessionFromContext(ctx)
	if err != nil {
		return *new(T), err
	}
	return One(ctx, s, q.mutation.Returning(q.projection.Columns...), q.projection)
}
