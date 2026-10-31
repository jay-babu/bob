# Bob next

An experimental, runnable redesign of Bob, developed alongside the existing
library. Existing Bob consumers continue to use their existing packages. This is
not yet a production replacement or a claim of feature parity.

The design keeps database-first Go types, composable SQL and explicit execution.
Its generated code contains schema facts and field mappings; its shared runtime
contains query behavior. The runtime imports only the standard library.

## Architecture

```text
database introspection ──> versioned schema document ──> table packages
                                   │                         │
                                   ├──> API/tooling           └──> shared runtime
                                   │
                                   └──> selected relationship packages
                                             │
                                             └──> endpoint table packages
```

Each table package contains a row, typed column bindings, separate insert/update
inputs, a full-row projection and its scanner. It imports no other table package.
Relationship packages connect endpoint tables without being imported by them.
Self-relations, reverse traversal and cycles therefore do not require cycles in
Go imports or embedded relationship graphs in every row.

This matters because the current fork's table-package generator already removes
reverse and multi-hop relationships, then drops remaining cyclic edges. This
implementation retains all included foreign keys in schema metadata and lets
applications explicitly select which relationship descriptors to generate.

Typed public expressions erase into a shared, nongeneric SQL tree. Predicates
work unchanged in select/update/delete operations. Generated code scales with
columns and selected relation descriptors, rather than generating every ORM
operation and relationship traversal for every table. Custom result types still
instantiate generic scanning/execution functions; measurements, not this design
alone, determine the compiler cost.

## Run the example

From the repository root:

```sh
go run ./next/cmd/bobgen \
  -schema next/examples/schema.json \
  -out next/examples/models \
  -import github.com/stephenafamo/bob/next/examples/models \
  -relations postsuser
go test ./next/...
```

The example packages are checked in, so ordinary tests compile their actual output.
The CLI reads the versioned document. `next/gen/driver.FromBob` adapts existing Bob
introspection results and type registrations; it does not make database connections.
Schema-specific enums/output types need an explicit application-owned type mapping.
Existing Bob configuration replacements and aliases are not automatically applied
by this adapter. Do not point the compact generator at existing Bob output.

For direct introspection using an existing driver's configuration:

```sh
go run ./next/cmd/bobgen-db -dialect psql -config bobgen.yaml \
  -out next-models -import example.org/project/next-models
```

This command connects to the explicitly configured database and reads its metadata;
it does not run migrations. Its `Configured` adapter applies registered types,
column type replacements, extra constraints and column aliases. Legacy custom
relationship rules are rejected and must be represented in the schema document.
`-type-map` accepts a JSON object mapping source type names to `go_type` and
`imports`, for example an application-owned enum type and its quoted Go import.
Direct introspection is tested against a disposable SQLite file. PostgreSQL and
MySQL use the existing introspection drivers and need live integration validation.

Generation renders every file before publication, skips identical files, writes
changed files with atomic rename, and removes only manifest-owned stale files.
It refuses to overwrite edited or unrelated files. Publication across multiple
files is not atomic; rerunning the same schema repairs an interrupted publication.
The manifest is an ownership record containing digests, not a second source copy.

## Queries and projections

```go
u := users.As("u")
p := u.Projection()
q := next.Select(u.Ref, p.Columns...).
    Where(u.Tenant.EQ(tenantID)).
    OrderBy(u.ID.Asc()).Limit(50)
rows, err := next.All(ctx, session, q, p)
```

Every table occurrence has a distinct binding. Out-of-scope references, shadowed
aliases and references to later joins are errors when building SQL. Parameter
numbering is allocated once for the complete tree, including correlated EXISTS.
Query values are immutable and can be reused independently.

Full-row projections select all fields. Smaller reads use a separate
`Projection[Result]` with explicit columns and a scan function for that result.
Before scanning, the executor checks ordered result labels against the projection.
It does not return a partially populated full row. Joined columns need distinct
labels. SQL-level expression validity remains the database's responsibility;
Go value types do not prove all SQL expressions or aggregates valid.

`next.SQL(text, args...)` provides trusted SQL-defined queries through the same
scanner, executor and observer. It uses native driver placeholders and never
interpolates values or rewrites SQL. Application result mappings for these queries
are checked at runtime; SQL text is not statically analyzed by this implementation.

## Relationships and pagination

Use `Relation.Exists` to filter by related rows without multiplying parents.
Use `JoinRelation` for explicit joins; uniqueness metadata determines whether the
target can multiply rows. A limit or offset over a multiplying join is rejected.
Arbitrary `JoinOn` joins are conservatively treated as multiplying.

`ParentCount(query)` counts distinct parent primary keys before pagination,
including when joins multiply rows. It rejects grouped queries rather than
guessing what constitutes a parent. `CountProjection()` scans its scalar result.

For collection expansion, page the parent query first and call `LoadMany` on those
parents. The loader deduplicates keys, handles comparable composite keys, preserves
parent order, and requires both a batch size and a total child-row bound. Its query
callback receives only the keys for that batch. It must include the intended
tenant scope and ordering. A returned child outside the batch is rejected.

Results are `WithMany[Parent, Child]` or `WithOne[Parent, Child]`. `Loaded.IsLoaded`
separates not requested from requested-but-absent. `LoadOne` rejects multiple
children. No field access issues SQL and no loader mutates parent models.

`SeekAfter` generates lexicographic predicates and matching ordering from typed
cursor values. It requires a nonnullable unique parent key in the ordering.
Nullable seek keys need an application null-ordering policy and are rejected.
The library does not automatically create snapshot consistency between the parent
and child reads; supply an appropriate transaction when a consistent snapshot matters.

## Writes, transactions and diagnostics

```go
input := users.Update{
    Name: next.Set("new name"),
    Note: next.Null[string](),
    Version: next.SetExpr(next.Add(u.Version.Expr(), next.Value(int64(1)))),
}
err := session.ExecOne(ctx,
    next.Update(u.Ref, input.Assignments(u)...).
        Where(u.Tenant.EQ(tenantID), u.ID.EQ(id), u.Version.EQ(version)))
```

Inputs distinguish omission, zero, NULL, DEFAULT and typed expressions. Generated
columns have read-only expression bindings and appear in neither write input.
The runtime additionally rejects unknown, repeated, generated and cross-table
assignments, missing required insert columns and unbounded update/delete calls.
`AllRows` explicitly permits an unbounded mutation. These are guards, not a proof
that an arbitrary predicate limits scope.

`OnConflict(...).DoUpdate(...)` validates known unique conflict targets;
`Excluded(column)` preserves the proposed value's Go type. PostgreSQL and SQLite
conflict updates are supported. MySQL conflict syntax and RETURNING, and SQLite
SET DEFAULT, are rejected rather than silently translated.

`ExecOne` reports `ErrNotMatched` for zero affected rows. It cannot distinguish a
missing record from a version conflict without another read. An unexpected
multirow write is reported *after execution*: use a transaction if that result
must cause rollback. Database constraints remain the concurrency authority.

An executor is supplied explicitly. `WithinTx` binds the callback session to its
transaction and rolls back on errors and panics. There are no hidden pool lookups,
nested transactions, automatic retries or save-on-load hooks. Native SQL and
generated queries use the same executor.

The observer reports SQL, parameter types, duration, scanned/affected rows,
recognized `sql.Tx` scope and sanitized failures. It excludes argument values and
raw driver error details. SQL itself is application source: putting sensitive
literals into raw SQL would expose them in an observer. PostgreSQL SQLSTATE errors
are classified while preserving the original error through `Unwrap`.

## Validation and adoption boundary

Tests cover real SQLite execution, projections, composite tenant keys, parent
pagination, keyset ordering, batch bounds, conflict writes, optimistic updates,
rollback, cursor cleanup, immutable composition, metadata copies, generator
ownership/recovery and relationship fanout. Negative compilation tests check
invalid comparison, assignment, nullability and cursor types. PostgreSQL/MySQL
SQL construction is tested; integration against their servers is still required.

The initial runtime does not reproduce Bob's complete dialect grammar, factory
generator, schema-enum generator, prepared-statement management, bulk-copy APIs or
automatic multi-hop graph attachment. Advanced CTEs, windows, set operations and
dialect-specific expressions can use the explicit SQL path. A production migration
also needs real-schema type mappings, permission-aware service tests and measured
behavior on representative Zeus services. Those are open adoption work, not
completed work or justification to replace the deployed library.

See [the build experiment](benchmarks/README.md) for reproducible measurements and
their limitations. Compiler-memory reduction remains unproven.
