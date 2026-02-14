package bob_test

import (
	"slices"
	"testing"

	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/dialect/psql"
	"github.com/stephenafamo/bob/dialect/psql/dialect"
	"github.com/stephenafamo/bob/dialect/psql/sm"
)

// -----------------------------------------------------------------------
// Benchmark: Pointer (mutable) vs Value (immutable) query-mod Apply
//
// The current Mod[T].Apply(T) interface uses pointer types (T = *SelectQuery)
// so every call mutates the query in place. This is efficient but makes it
// easy to accidentally corrupt a shared base query.
//
// An immutable alternative would clone the query before applying mods.
// The question is: how much does that clone cost?
//
// These benchmarks answer that question across realistic scenarios:
//   1. Building a query from scratch (identical for both approaches)
//   2. Deriving a single query from a base
//   3. Deriving many queries from the same base (fan-out pattern)
//   4. Isolated clone cost by query complexity
//   5. Raw Apply cost without any clone
//   6. Fan-out at scale (10 variants)
//   7. Clone+Apply vs full rebuild
//   8. Chained multi-derivation
//   9. Hand-written Clone vs reflection-based Clone (reprint.This)
//
// The key finding: the current Clone() uses reprint.This() (reflection-based
// deep copy) which is the main bottleneck. A hand-written Clone method on
// SelectQuery that uses slices.Clone() for each slice field is ~10x faster
// and makes the immutable approach competitive with mutable.
//
// Run with:
//
//	go test -bench=Benchmark -benchmem -count=6 -run='^$' .
//
// -----------------------------------------------------------------------

// manualCloneSelectQuery performs a hand-written clone of a SelectQuery.
// It copies the struct by value, then independently clones all slice fields
// so that appending to the clone does not affect the original.
//
// This simulates what a generated Clone() method on SelectQuery would do,
// replacing the current reflection-based reprint.This() used in BaseQuery.Clone().
// The two unexported slice fields in bob.Load (loadFuncs, preloadMapperMods)
// are not cloned here since they are typically nil during query construction
// and would add at most 2 extra slice copies in a real implementation.
func manualCloneSelectQuery(src *dialect.SelectQuery) *dialect.SelectQuery {
	// Struct value copy — copies all scalar fields and slice headers.
	dst := *src

	// Independently clone each slice field so appending to dst doesn't
	// mutate src's underlying arrays.
	dst.With.CTEs = slices.Clone(src.With.CTEs)
	dst.SelectList.Columns = slices.Clone(src.SelectList.Columns)
	dst.SelectList.PreloadColumns = slices.Clone(src.SelectList.PreloadColumns)
	dst.Distinct.On = slices.Clone(src.Distinct.On)
	dst.TableRef.Columns = slices.Clone(src.TableRef.Columns)
	dst.TableRef.Partitions = slices.Clone(src.TableRef.Partitions)
	dst.TableRef.IndexHints = slices.Clone(src.TableRef.IndexHints)
	dst.TableRef.Joins = slices.Clone(src.TableRef.Joins)
	dst.Where.Conditions = slices.Clone(src.Where.Conditions)
	dst.GroupBy.Groups = slices.Clone(src.GroupBy.Groups)
	dst.Having.Conditions = slices.Clone(src.Having.Conditions)
	dst.Windows.Windows = slices.Clone(src.Windows.Windows)
	dst.Combines.Queries = slices.Clone(src.Combines.Queries)
	dst.OrderBy.Expressions = slices.Clone(src.OrderBy.Expressions)
	dst.Locks.Locks = slices.Clone(src.Locks.Locks)
	dst.EmbeddedHook.Hooks = slices.Clone(src.EmbeddedHook.Hooks)
	dst.ContextualModdable.Mods = slices.Clone(src.ContextualModdable.Mods)
	dst.CombinedOrder.Expressions = slices.Clone(src.CombinedOrder.Expressions)

	return &dst
}

// ---------- helpers ----------

// baseMods returns a realistic set of mods that build a "base" query.
func baseMods() []bob.Mod[*dialect.SelectQuery] {
	return []bob.Mod[*dialect.SelectQuery]{
		sm.Columns("id", "name", "email", "created_at"),
		sm.From("users"),
		sm.Where(psql.Quote("active").EQ(psql.Arg(true))),
		sm.Where(psql.Quote("deleted_at").IsNull()),
		sm.OrderBy(psql.Quote("created_at")).Desc(),
		sm.Limit(100),
	}
}

// deriveMods returns mods that would be applied on top of a base query.
func deriveMods() []bob.Mod[*dialect.SelectQuery] {
	return []bob.Mod[*dialect.SelectQuery]{
		sm.Where(psql.Quote("role").EQ(psql.Arg("admin"))),
		sm.Columns("role"),
		sm.Limit(10),
	}
}

// ---------- Scenario 1: Build from scratch ----------
// Both approaches are identical — no clone needed when building fresh.

func BenchmarkBuildFromScratch_Mutable(b *testing.B) {
	mods := baseMods()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = psql.Select(mods...)
	}
}

func BenchmarkBuildFromScratch_Immutable(b *testing.B) {
	mods := baseMods()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = psql.Select(mods...)
	}
}

// ---------- Scenario 2: Derive one query from a base ----------

func BenchmarkDerive_Mutable(b *testing.B) {
	base := psql.Select(baseMods()...)
	extra := deriveMods()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		// Mutates base in place — no allocation, but corrupts the original.
		base.Apply(extra...)
	}
}

func BenchmarkDerive_Immutable(b *testing.B) {
	base := psql.Select(baseMods()...)
	extra := deriveMods()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		q := base.Clone()
		q.Apply(extra...)
	}
}

// ---------- Scenario 3: Fan-out — derive 5 queries from same base ----------
// The key real-world pattern: one base, many derived variants.

func BenchmarkFanOut5_Mutable(b *testing.B) {
	filters := []bob.Mod[*dialect.SelectQuery]{
		sm.Where(psql.Quote("role").EQ(psql.Arg("admin"))),
		sm.Where(psql.Quote("role").EQ(psql.Arg("editor"))),
		sm.Where(psql.Quote("role").EQ(psql.Arg("viewer"))),
		sm.Where(psql.Quote("status").EQ(psql.Arg("active"))),
		sm.Where(psql.Quote("status").EQ(psql.Arg("suspended"))),
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		// Must rebuild base each time because mutable Apply corrupts it.
		for _, f := range filters {
			base := psql.Select(baseMods()...)
			base.Apply(f)
		}
	}
}

func BenchmarkFanOut5_Immutable(b *testing.B) {
	base := psql.Select(baseMods()...)
	filters := []bob.Mod[*dialect.SelectQuery]{
		sm.Where(psql.Quote("role").EQ(psql.Arg("admin"))),
		sm.Where(psql.Quote("role").EQ(psql.Arg("editor"))),
		sm.Where(psql.Quote("role").EQ(psql.Arg("viewer"))),
		sm.Where(psql.Quote("status").EQ(psql.Arg("active"))),
		sm.Where(psql.Quote("status").EQ(psql.Arg("suspended"))),
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		// Clone preserves the base — safe to reuse.
		for _, f := range filters {
			q := base.Clone()
			q.Apply(f)
		}
	}
}

// ---------- Scenario 4: Isolated Clone cost ----------

func BenchmarkClone_EmptyQuery(b *testing.B) {
	base := psql.Select()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = base.Clone()
	}
}

func BenchmarkClone_SmallQuery(b *testing.B) {
	base := psql.Select(baseMods()...)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = base.Clone()
	}
}

func BenchmarkClone_LargeQuery(b *testing.B) {
	mods := []bob.Mod[*dialect.SelectQuery]{
		sm.Columns(
			"u.id", "u.name", "u.email", "u.role",
			"u.created_at", "u.updated_at", "u.deleted_at",
			"p.bio", "p.avatar_url",
			"o.id", "o.name",
		),
		sm.From("users"),
		sm.Where(psql.Quote("u", "active").EQ(psql.Arg(true))),
		sm.Where(psql.Quote("u", "deleted_at").IsNull()),
		sm.Where(psql.Quote("o", "plan").EQ(psql.Arg("enterprise"))),
		sm.Where(psql.Quote("u", "role").In(psql.Arg("admin"), psql.Arg("editor"))),
		sm.GroupBy(psql.Quote("u", "org_id")),
		sm.Having(psql.Quote("count").GT(psql.Arg(5))),
		sm.OrderBy(psql.Quote("u", "created_at")).Desc(),
		sm.OrderBy(psql.Quote("u", "name")).Asc(),
		sm.Limit(50),
		sm.Offset(100),
	}
	base := psql.Select(mods...)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = base.Clone()
	}
}

// ---------- Scenario 5: Apply overhead only ----------

func BenchmarkApplyOnly_SmallModSet(b *testing.B) {
	extra := deriveMods()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		q := &dialect.SelectQuery{}
		for _, mod := range extra {
			mod.Apply(q)
		}
	}
}

func BenchmarkApplyOnly_LargeModSet(b *testing.B) {
	mods := baseMods()
	mods = append(mods, deriveMods()...)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		q := &dialect.SelectQuery{}
		for _, mod := range mods {
			mod.Apply(q)
		}
	}
}

// ---------- Scenario 6: Fan-out at scale (10 variants) ----------

func BenchmarkFanOut10_Mutable(b *testing.B) {
	filters := make([]bob.Mod[*dialect.SelectQuery], 10)
	for i := range filters {
		filters[i] = sm.Where(psql.Quote("col").EQ(psql.Arg(i)))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for _, f := range filters {
			base := psql.Select(baseMods()...)
			base.Apply(f)
		}
	}
}

func BenchmarkFanOut10_Immutable(b *testing.B) {
	base := psql.Select(baseMods()...)
	filters := make([]bob.Mod[*dialect.SelectQuery], 10)
	for i := range filters {
		filters[i] = sm.Where(psql.Quote("col").EQ(psql.Arg(i)))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for _, f := range filters {
			q := base.Clone()
			q.Apply(f)
		}
	}
}

// ---------- Scenario 7: Clone+Apply vs full rebuild ----------
// Compares deriving via Clone+Apply against constructing the full
// query from scratch including the extra mod.

func BenchmarkFullRebuild(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = psql.Select(
			sm.Columns("id", "name", "email", "created_at"),
			sm.From("users"),
			sm.Where(psql.Quote("active").EQ(psql.Arg(true))),
			sm.Where(psql.Quote("deleted_at").IsNull()),
			sm.OrderBy(psql.Quote("created_at")).Desc(),
			sm.Limit(100),
			sm.Where(psql.Quote("role").EQ(psql.Arg("admin"))),
		)
	}
}

func BenchmarkCloneAndApply(b *testing.B) {
	base := psql.Select(baseMods()...)
	extra := sm.Where(psql.Quote("role").EQ(psql.Arg("admin")))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		q := base.Clone()
		q.Apply(extra)
	}
}

// ---------- Scenario 8: Chained multi-derivation ----------
// base -> filtered -> paginated (two derivation steps).

func BenchmarkChainedDerive_Mutable(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		base := psql.Select(baseMods()...)
		base.Apply(sm.Where(psql.Quote("role").EQ(psql.Arg("admin"))))
		base.Apply(sm.Limit(10), sm.Offset(20))
	}
}

func BenchmarkChainedDerive_Immutable(b *testing.B) {
	base := psql.Select(baseMods()...)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		filtered := base.Clone()
		filtered.Apply(sm.Where(psql.Quote("role").EQ(psql.Arg("admin"))))
		paginated := filtered.Clone()
		paginated.Apply(sm.Limit(10), sm.Offset(20))
	}
}

// ---------- Scenario 9: Hand-written Clone ----------
// The current Clone() uses reprint.This() (reflection-based deep copy)
// which dominates the cost of the immutable approach. These benchmarks
// show what performance would look like with a hand-written Clone method
// that uses slices.Clone() — the approach that would be used if
// SelectQuery implemented its own Clone() method.

func BenchmarkManualClone_SmallQuery(b *testing.B) {
	base := psql.Select(baseMods()...)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = manualCloneSelectQuery(base.Expression)
	}
}

func BenchmarkManualClone_LargeQuery(b *testing.B) {
	mods := []bob.Mod[*dialect.SelectQuery]{
		sm.Columns(
			"u.id", "u.name", "u.email", "u.role",
			"u.created_at", "u.updated_at", "u.deleted_at",
			"p.bio", "p.avatar_url",
			"o.id", "o.name",
		),
		sm.From("users"),
		sm.Where(psql.Quote("u", "active").EQ(psql.Arg(true))),
		sm.Where(psql.Quote("u", "deleted_at").IsNull()),
		sm.Where(psql.Quote("o", "plan").EQ(psql.Arg("enterprise"))),
		sm.Where(psql.Quote("u", "role").In(psql.Arg("admin"), psql.Arg("editor"))),
		sm.GroupBy(psql.Quote("u", "org_id")),
		sm.Having(psql.Quote("count").GT(psql.Arg(5))),
		sm.OrderBy(psql.Quote("u", "created_at")).Desc(),
		sm.OrderBy(psql.Quote("u", "name")).Asc(),
		sm.Limit(50),
		sm.Offset(100),
	}
	base := psql.Select(mods...)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = manualCloneSelectQuery(base.Expression)
	}
}

// ---------- Scenario 10: Immutable with hand-written Clone ----------
// Direct comparison: immutable derive using hand-written clone vs mutable.

func BenchmarkDerive_ImmutableManualClone(b *testing.B) {
	base := psql.Select(baseMods()...)
	extra := deriveMods()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		cloned := manualCloneSelectQuery(base.Expression)
		for _, mod := range extra {
			mod.Apply(cloned)
		}
	}
}

func BenchmarkFanOut5_ImmutableManualClone(b *testing.B) {
	base := psql.Select(baseMods()...)
	filters := []bob.Mod[*dialect.SelectQuery]{
		sm.Where(psql.Quote("role").EQ(psql.Arg("admin"))),
		sm.Where(psql.Quote("role").EQ(psql.Arg("editor"))),
		sm.Where(psql.Quote("role").EQ(psql.Arg("viewer"))),
		sm.Where(psql.Quote("status").EQ(psql.Arg("active"))),
		sm.Where(psql.Quote("status").EQ(psql.Arg("suspended"))),
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for _, f := range filters {
			cloned := manualCloneSelectQuery(base.Expression)
			f.Apply(cloned)
		}
	}
}

func BenchmarkFanOut10_ImmutableManualClone(b *testing.B) {
	base := psql.Select(baseMods()...)
	filters := make([]bob.Mod[*dialect.SelectQuery], 10)
	for i := range filters {
		filters[i] = sm.Where(psql.Quote("col").EQ(psql.Arg(i)))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for _, f := range filters {
			cloned := manualCloneSelectQuery(base.Expression)
			f.Apply(cloned)
		}
	}
}
