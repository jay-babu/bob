# Functional requirements and build restrictions

The new design does not inherit the old contracts as constraints. This inventory
separates useful behavior from measures introduced to make a large schema compile.

| Requirement | New design |
| --- | --- |
| Forward, reverse and cyclic relationships | One FK descriptor supports both directions through `Reverse`; rows contain no graph fields. Descriptors and joined projections compose across schemas. |
| Large, heavily referenced audit tables | Retained in metadata and generated models. Incoming relationships do not enlarge or change the referenced row definition. |
| Scoped/pinned join aliases | Table occurrences have identity independent of alias strings. Repeated hints receive distinct SQL names, including correlated scopes. |
| PostgreSQL identifier limits | Deterministic bounded aliases and result labels retain UTF-8 boundaries and distinguish long paths. |
| JSON v2 defaults | Structured `JSON[T]` uses v2 defaults, including duplicate-member rejection and nil collection defaults. |
| Independent JSON scan results | Decoding uses fresh storage so reused scan targets cannot mutate earlier maps/slices or retain omitted fields. |
| Execution in an existing transaction | An explicit Session carries the executor through queries and loaders; there is no implicit pool fallback. |
| Typed scanners and projections | Generated scanners map full rows directly. Custom result types have explicit ordered projections. |
| Nested/expand-style data access | Explicit, composable bounded loaders and full relationship metadata support application-owned expansion plans; field access never performs SQL. |
| Typed predicates across reads and writes | One expression representation works for SELECT/UPDATE/DELETE without per-operation generated filter families. |

The former restrictions are not part of the relationship model: reverse edges,
multi-hop query composition and cyclic schema facts are not dropped. The default
output does not require table packages. Optional table layout trades package
count for narrow imports and schema-change caching, and does not change SQL
capabilities or the available relationships.

Scoped factory generators and transport/API generators remain tooling concerns,
not production model dependencies. This branch supplies typed insert inputs,
schema facts and emitted Go bindings for such tools. It does not claim that every
old factory or string-expansion convenience API has a new counterpart already.
Advanced SQL uses the common explicit SQL executor instead of a second ORM path.
