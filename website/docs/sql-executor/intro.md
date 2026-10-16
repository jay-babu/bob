---

sidebar_position: 0
description: Understand the `bob.Executor` interface and the provided types

---

import DocCardList from '@theme/DocCardList';
import {useCurrentSidebarCategory} from '@docusaurus/theme-common';

# Introduction

Bob's Executor is an interface that looks like this.

```go
type Executor interface {
	QueryContext(ctx context.Context, query string, args ...any) (scan.Rows, error)
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}
```

This executor is used to run queries

<DocCardList items={useCurrentSidebarCategory().items.filter(i => i.label != 'Introduction')} />

## Using an executor from context

Attach an executor once with `bob.WithExecutor`, then use the context-only
operations in helpers that receive that context:

```go
func firstID(ctx context.Context) (int, error) {
    q := psql.Select(sm.Columns("id"), sm.From("items"))
    return bob.OneContext(ctx, q, scan.SingleColumnMapper[int])
}

// db is a bob.DB. The caller still owns the transaction.
tx, err := db.Begin(ctx)
if err != nil {
    return err
}
defer tx.Rollback(ctx)

txCtx := bob.WithExecutor(ctx, tx)
id, err := firstID(txCtx)
if err != nil {
    return err
}
// Use id before committing.
_ = id
return tx.Commit(ctx)
```

Generated model queries expose `OneContext(ctx)`, `AllContext(ctx)`,
`ExecContext(ctx)`, `CursorContext(ctx)`, and `EachContext(ctx)` alongside their
existing explicit-executor methods. View queries also provide
`CountContext(ctx)` and `ExistsContext(ctx)`. For example:

```go
user, err := models.Users.Query().OneContext(txCtx)
```

For query builders and raw queries, use `bob.OneContext`, `bob.AllContext`,
`bob.AllxContext`, `bob.ExecContext`, `bob.CursorContext`, or `bob.EachContext`.
Base and named-bound queries also expose an `ExecContext(ctx)` method.

These operations return `bob.ErrNoExecutor` when the context has no executor.
They do not fall back to a global database. `bob.ExecutorFromContext` lets
custom helpers retrieve the attached executor explicitly. Existing methods
such as `One(ctx, exec)` continue to use their explicit executor.

`WithExecutor` returns a child context; it does not change its parent or open,
commit, or roll back a transaction. A child may override its parent's executor;
passing a nil executor masks the parent's executor. Always pass a valid executor
for execution, and use transaction contexts, cursors, and iterators only while
their transaction is open. Consume or close cursors before committing or rolling
back. Avoid sharing a transaction between concurrent operations unless its driver
supports that usage.

This API covers query execution. Model instance, relationship, and prepared
statement APIs retain their existing signatures.
