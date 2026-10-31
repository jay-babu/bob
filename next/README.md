# Bob next

A PostgreSQL-first SQL toolkit designed for large schemas. This is a new API;
compatibility with the previous Bob contracts is not a design constraint. The
previous implementation remains available as a benchmark baseline.

The targets are small generated code, predictable compilation, a small linked
runtime, full-row reads and SQL whose cardinality is visible to callers.
Database constraints remain the authority for concurrency and data integrity.

## Architecture

```text
PostgreSQL catalogs --> versioned schema metadata --> model package(s)
                                  |                         |
                                  +--> API/tooling           +--> shared runtime
                                  |
                                  +--> independent relationship descriptors
```

Generation defaults to one model package containing namespaced rows, columns,
insert/update inputs and scanners. It does not embed relationships into rows.
The `public` schema is implicit in Go model names: `public.cohort_item` generates
`CohortItemRow`, `CohortItemAs` and `CohortItemUpdate`. SQL metadata retains the
schema, and other schemas retain their name prefixes to distinguish tables.
Relationship descriptors import model definitions; models never import descriptor
packages. Cycles, reverse edges and heavily referenced audit tables are retained.

`-layout tables` emits independent table packages for services that need a narrow
subset and fine-grained schema-change caching. This is a layout choice, not a
requirement for representing the relationship graph. Both layouts use the same
runtime, projections and metadata contract.

Generated row projections return pointers. This avoids copying wide rows and
lets generic execution code share a pointer shape across different tables. The
cursor loop, projection validation, execution, error handling and observation
are nongeneric shared code. Generics remain at the typed value boundary.

JSON columns use `json.RawMessage` by default. Application-owned JSON structures
belong in application projections or `next.JSON[T]` scanners, rather than making
the entire database model package import an API model dependency graph.

## Generate from PostgreSQL

Set `BOB_NEXT_DSN` to the intended database connection string, then run:

```sh
go run ./next/cmd/bobgen-db \
  -schemas public,reference,audit,scrape \
  -out models -import example.org/project/models -relations all
```

The command opens a read-only, repeatable-read transaction and reads catalog
facts in five queries, independent of table count. It does not run migrations.
The schema allowlist is explicit. Partition children are omitted by default;
logical partitioned parents are included. `postgres.Options.IncludePartitions`
allows callers of the inspection library to request physical partition objects.
Views, materialized views and foreign tables are read-only in generated APIs.

Enums, domains and opaque extension/range types receive independent packages.
Domains retain distinct Go types and their inherited nullability/default facts.
Ordinary valid, nonpartial unique indexes are recognized, including indexes with
INCLUDE columns; partial or expression indexes are not assumed to prove a
column-based to-one relationship.

Unknown SQL types use distinct textual values rather than imposing extension
SDKs on every model consumer. `-type-map` optionally supplies application-owned
Go types keyed by qualified PostgreSQL type name. Each JSON mapping has a
`go_type` and quoted Go `imports`. There are no legacy configuration aliases,
replacement rules, ORM plugins or generator-output dependencies in this path.

Generation writes versioned `schema.json` facts and `go-bindings.json` output
symbols. The table `package` field is a logical naming hint; bindings give the
actual import paths, row types and factory functions for the selected layout.
These files are the integration contract for API generators and schema tooling. To regenerate offline:

```sh
go run ./next/cmd/bobgen -schema models/schema.json \
  -out models -import example.org/project/models -relations all
```

Omit `-relations` to generate no descriptor packages, or provide a comma-separated
allowlist. Every included FK stays in metadata regardless of that choice.
Unchanged files retain their timestamps; changed files use atomic rename.
A digest manifest owns cleanup and protects unrelated or edited files. Publication
across multiple files is not transactional; repeating the same input repairs an
interrupted publication.

## Queries and result shapes

The checked-in example can be generated and tested without a database:

```sh
go run ./next/cmd/bobgen -schema next/examples/schema.json \
  -out next/examples/models \
  -import github.com/stephenafamo/bob/next/examples/models -relations postsuser
go test -short ./next/...
```

```go
u := models.UsersAs("u")
rows, err := u.Query().
    Where(u.Tenant.EQ(tenantID)).OrderBy(u.ID.Asc()).Limit(50).
    AllContext(ctx) // []*models.UsersRow
```

`Query()` attaches the generated full-row scanner automatically. Its shared
`RowQuery[T]` wrapper keeps the row type through filters, joins, ordering and
pagination. `OneContext`, `EachContext` and `CountContext` use the same binding.
Normal table reads require neither a session nor an explicit projection.

Writes keep their full-row result types attached as well:

```go
row, err := u.Insert(models.UsersInsert{
    ID: userID, Tenant: tenantID, Name: "New user",
}).OneContext(ctx)

err = u.Update(models.UsersUpdate{Name: next.Set("Renamed")}).
    Where(u.Tenant.EQ(tenantID), u.ID.EQ(userID)).
    ExecOneContext(ctx)
```

`OneContext` and `AllContext` request full rows with `RETURNING`; `ExecContext`
and `ExecOneContext` execute without returning columns. Read-only views expose
neither insert, update nor delete methods.

The database integration binds its executor once. With `database/sql`:

```go
ctx = next.WithExecutor(ctx, tx, next.ExecutionConfig{})
```

For Zeus-style DBExec, replace its existing `bob.WithExecutor` binding with the
optional `next/bobexec` adapter at the transaction boundary:

```go
ctx = bobexec.WithExecutor(ctx, tx, next.ExecutionConfig{
    Dialect: next.Postgres, InTransaction: true,
})
```

This binds the same Bob executor for both APIs, including existing pgx wrappers,
debug wrappers and auditing configuration. It does not open a connection or own
commit/rollback. Existing `bob.WithExecutor` bindings alone do not bind next;
the adapter must be installed at the integration boundary. Zeus integration is
not included in this repository. Missing executors return `ErrNoExecutor`;
nil bindings mask parent bindings and expired transactions never fall back to
a pool. Contexts must stay within their executor's lifetime.

Each table occurrence has a distinct binding. Out-of-scope references, reused table bindings and references to later joins
fail when building SQL. Repeated alias hints receive distinct SQL names, so
independent fragments compose without accidental shadowing. PostgreSQL aliases
and result labels are bounded without splitting UTF-8 or collapsing join paths. Parameters are
numbered once across the complete tree, including correlated EXISTS. Query values
are immutable and independently reusable.

A full-row projection selects every field. Smaller reads use a separate
`Projection[Result]` with an explicit scanner for that result. Ordered, unique
result labels are checked before scanning; unselected fields are not silently
returned as zero-valued fields of a full model. Joined fields need distinct labels.
Go value types do not prove every SQL expression or aggregate semantically valid.

`next.SQL(text, args...)` provides trusted SQL-defined queries through the same
executor, projection, transaction and observer. It supports advanced PostgreSQL
CTEs, window functions and other grammar without building a second execution
path. It accepts native placeholders and never interpolates values. SQL-defined
query result mappings are checked at runtime, not statically inferred from SQL.

## Cardinality, loading and pagination

`Relation.Exists` filters parents without multiplying them. `JoinRelation` uses
uniqueness metadata to identify to-one joins. LIMIT/OFFSET over a multiplying
join is rejected. Arbitrary `JoinOn` joins conservatively have unknown multiplicity.

`ParentCount(query)` counts distinct parent primary keys before pagination and
rejects grouped queries instead of guessing their parent semantics. Its result
uses `CountProjection()`.

For collections, page parents first, then call `LoadMany`. The loader deduplicates
comparable composite keys, preserves parent order, and requires a batch size and
a total child-row limit. Query callbacks include the intended scope and ordering.
A child outside its batch is rejected, including a child from another tenant
whose ordinary ID happens to match. `LoadOne` rejects multiple children.

Results have an explicit `Loaded` state, distinguishing not requested from loaded
and absent. Field access performs no SQL. Loading mutates no parent model. Supply
an appropriate transaction when parent and child reads need one consistent
snapshot; separate reads do not acquire snapshot consistency automatically.

`SeekAfter` produces matching lexicographic predicates and ordering from typed
cursor values. Its nonnullable ordering must include a unique parent key. Nullable
seek ordering requires an explicit policy and is deliberately rejected.

## Values, writes and transactions

`Decimal` retains exact numeric text, including scale, without a binary floating
point conversion. SQL arithmetic stays on the server. Its JSON representation
is a string, preserving precision through ordinary JSON consumers.

`Array[T]` preserves dimensions, lower bounds, and NULL elements. SQL NULL is
separate through `sql.Null[Array[T]]`. Time values parsed from array text normalize
to UTC. `JSON[T]` provides typed application JSON with JSON v2 defaults and fresh decode
storage on every scan. Reused scan destinations never mutate earlier map/slice
values or retain omitted fields. SQL NULL is separate from JSON null. Driver Valuer arguments are resolved at the execution boundary, preventing
pgx's fallback array encoding from silently resetting lower bounds.

Insert/update inputs distinguish omission, zero, NULL, DEFAULT and expressions.
Generated columns expose read-only expressions and appear in neither write input.
The runtime rejects missing required insert fields, invalid assignments, and
writes through read-only descriptors. UPDATE/DELETE require a predicate or an
explicit `AllRows`; an arbitrary predicate is not a proof of narrow authorization.

`OnConflict(...).DoUpdate(...)` requires a known unique conflict target, and
`Excluded(column)` retains the proposed value's Go type. `ExecOne` reports
`ErrNotMatched` when no row matches; it cannot distinguish missing rows from a
failed version predicate without another read. An unexpected multirow write is
reported after execution: use a transaction when that outcome must roll back.

Execution uses the context-bound executor or the lower-level explicit API.
`ExecContext` and `ExecOneContext` execute writes without a session argument.
`WithinTx` binds all callback operations to its
transaction and rolls back on callback errors or panics. There are no hidden pool
lookups, save-on-load hooks, automatic retries or transaction creation on reads.

The observer exposes SQL, parameter types, duration, row counts, recognized
`sql.Tx` scope and sanitized failures. Values and raw driver error details are
excluded. SQL text is application source: sensitive literals embedded in raw SQL
would still appear in that SQL text. SQLSTATE classification preserves the
original PostgreSQL error through `Unwrap`. Cursors close on errors and panics.

## Validation and trial boundary

```sh
# Uses a cached image and creates only private, disposable socket-only servers.
docker pull postgres:17-bookworm
BOB_NEXT_REQUIRE_POSTGRES=1 go test -race ./next/...
go vet ./next/...
```

PostgreSQL integration verifies exact values, generated enum/domain/array/range
code, composite foreign keys, conflict writes, optimistic predicates, pagination,
loading, error classification and rollback. Negative compilation tests check
invalid value, nullability, cursor and generated-column writes. Array tests
include a round-trip fuzz target. CI requires PostgreSQL integration to run;
a missing test image is not counted as a pass there.

The current local Zeus test schema has also been generated, including the
previously excluded audit-reason table, and every generated package compiled.
The private schema and generated files are not bundled in this repository.
See [build measurements](benchmarks/README.md) for the comparison and limitations,
and [fork feature decisions](FEATURES.md) for functional requirements versus
size-driven restrictions.

The branch is intended for isolated application trials before any deployment.
It defines a PostgreSQL-first contract, not complete cross-dialect guarantees.
Basic SQLite execution and SQL rendering remain useful for tests; MySQL server
integration is not claimed. Factory generation, a transport/API generator and a
service migration are separate tools or application work, not compatibility goals
for this runtime. Advanced SQL can use the common explicit SQL execution path.
