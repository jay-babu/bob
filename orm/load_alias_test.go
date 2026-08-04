package orm

import (
	"strings"
	"testing"

	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/clause"
	"github.com/stephenafamo/bob/expr"
	"github.com/stephenafamo/scan"
)

func TestCapPreloadAlias(t *testing.T) {
	cols := []string{"id", "changed_reason"} // maxCol = 14

	cases := map[string]struct {
		alias string
		cols  []string
	}{
		"within budget":     {alias: "campaign_participant.loyalty_customer", cols: cols},
		"exactly at budget": {alias: strings.Repeat("a", 63-1-14), cols: cols},
		"one over budget":   {alias: strings.Repeat("a", 63-14), cols: cols},
		"far over budget":   {alias: strings.Repeat("parent.", 20) + "changed_reason", cols: cols},
		"no columns":        {alias: strings.Repeat("a", 100), cols: nil},
		"column too wide":   {alias: strings.Repeat("a", 70), cols: []string{strings.Repeat("c", 60)}},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			maxCol := 0
			for _, c := range tc.cols {
				if len(c) > maxCol {
					maxCol = len(c)
				}
			}

			got := capPreloadAlias(tc.alias, tc.cols)

			if len(tc.alias)+1+maxCol <= 63 {
				if got != tc.alias {
					t.Fatalf("alias within budget was modified: %q -> %q", tc.alias, got)
				}
				return
			}

			// 53 = 63 - len("_") - 8 hex chars - len(".") - a 1-byte column;
			// wider columns shrink the kept prefix further, so the cap itself
			// can only be shorter.
			if len(got)+1+maxCol > 63 && maxCol <= 53 {
				t.Fatalf("capped alias %q (len %d) + %q still exceeds 63 bytes", got, len(got), tc.cols)
			}
			if !strings.HasPrefix(tc.alias, strings.SplitN(got, "_", 2)[0]) {
				t.Fatalf("capped alias %q does not preserve a prefix of %q", got, tc.alias)
			}
			if again := capPreloadAlias(tc.alias, tc.cols); again != got {
				t.Fatalf("not deterministic: %q then %q", got, again)
			}
		})
	}
}

func TestCapPreloadAliasDistinguishesSharedPrefixes(t *testing.T) {
	// Sibling paths that agree beyond the kept-prefix cut must still get
	// distinct aliases — the hash of the full path is the disambiguator.
	base := strings.Repeat("parent.", 10)
	cols := []string{"id"}

	a := capPreloadAlias(base+"loyalty_customer", cols)
	b := capPreloadAlias(base+"loyalty_customer_rank", cols)

	if a == b {
		t.Fatalf("distinct paths collided on %q", a)
	}
}

// capturePreloadQuery records what a preloader appends so tests can inspect
// the generated join alias and select-column prefix.
type capturePreloadQuery struct {
	joins   []clause.Join
	selects []any
}

func (q *capturePreloadQuery) AppendLoader(...bob.Loader)     {}
func (q *capturePreloadQuery) AppendMapperMod(scan.MapperMod) {}
func (q *capturePreloadQuery) AppendJoin(j clause.Join)       { q.joins = append(q.joins, j) }
func (q *capturePreloadQuery) AppendPreloadSelect(columns ...any) {
	q.selects = append(q.selects, columns...)
}

var _ PreloadableQuery = (*capturePreloadQuery)(nil)

// TestPreloadNestedAliasCapped drives Preload with a parent alias long enough
// to overflow and checks the join alias and the selected-column prefix are the
// same capped value — the invariant that keeps SQL and scanner in agreement.
func TestPreloadNestedAliasCapped(t *testing.T) {
	rel := PreloadRel[bob.Expression]{
		Name: "Child",
		Sides: []PreloadSide[bob.Expression]{{
			From:        testNameable{name: "parents", alias: "parents"},
			To:          testNameable{name: "children", alias: "children"},
			FromColumns: []string{"child_id"},
			ToColumns:   []string{"id"},
		}},
	}
	cols := []string{"id", "name"}

	loader := Preload[*testPreloadChild, testPreloadChildSlice](
		rel, cols, nil, PreloadAs[*capturePreloadQuery]("changed_reason"),
	)

	parent := "campaign_participant." + strings.Repeat("loyalty_customer.", 3) + "customer"
	mod, _, _ := loader(parent)

	q := new(capturePreloadQuery)
	mod.Apply(q)

	if len(q.joins) != 1 {
		t.Fatalf("expected 1 join, got %d", len(q.joins))
	}
	alias := q.joins[0].To.Alias

	want := capPreloadAlias(parent+".changed_reason", cols)
	if alias != want {
		t.Fatalf("join alias %q, want capped alias %q", alias, want)
	}
	if alias == parent+".changed_reason" {
		t.Fatalf("test alias %q did not overflow; lengthen the parent chain", alias)
	}
	if len(alias)+1+len("name") > 63 {
		t.Fatalf("join alias %q still produces >63-byte column labels", alias)
	}

	if len(q.selects) != 1 {
		t.Fatalf("expected 1 preload select, got %d", len(q.selects))
	}
	colsExpr, ok := q.selects[0].(expr.ColumnsExpr)
	if !ok {
		t.Fatalf("preload select is %T, want expr.ColumnsExpr", q.selects[0])
	}
	if got := colsExpr.AliasPrefix(); got != alias+"." {
		t.Fatalf("column alias prefix %q does not match join alias %q", got, alias+".")
	}
}
