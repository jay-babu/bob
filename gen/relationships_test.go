package gen

import (
	"testing"

	"github.com/stephenafamo/bob/gen/drivers"
	"github.com/stephenafamo/bob/orm"
	"github.com/volatiletech/strmangle"
)

func TestJoinTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		Pkey   []string
		Fkey   []string
		Should bool
	}{
		{Pkey: []string{"one", "two"}, Fkey: []string{"one", "two"}, Should: true},
		{Pkey: []string{"two", "one"}, Fkey: []string{"one", "two"}, Should: true},

		{Pkey: []string{"one"}, Fkey: []string{"one"}, Should: false},
		{Pkey: []string{"one", "two", "three"}, Fkey: []string{"one", "two"}, Should: false},
		{Pkey: []string{"one", "two", "three"}, Fkey: []string{"one", "two", "three"}, Should: false},
		{Pkey: []string{"one"}, Fkey: []string{"one", "two"}, Should: false},
		{Pkey: []string{"one", "two"}, Fkey: []string{"one"}, Should: false},
	}

	for i, test := range tests {
		var table drivers.Table[any, any]

		table.Constraints.Primary = &drivers.Constraint[any]{Columns: test.Pkey}
		for _, col := range strmangle.SetMerge(test.Pkey, test.Fkey) {
			table.Columns = append(table.Columns, drivers.Column{Name: col})
		}
		for _, k := range test.Fkey {
			table.Constraints.Foreign = append(
				table.Constraints.Foreign,
				drivers.ForeignKey[any]{
					Constraint: drivers.Constraint[any]{Columns: []string{k}},
				},
			)
		}

		if table.IsJoinTable() != test.Should {
			t.Errorf("%d) want: %t, got: %t\nTest: %#v", i, test.Should, !test.Should, test)
		}
	}
}

func TestFilterGeneratedRelationships(t *testing.T) {
	t.Parallel()

	relationships := Relationships{
		"child": {
			{
				Name: "child_parent_fk",
				Sides: []orm.RelSide{{
					From:        "child",
					FromColumns: []string{"parent_id"},
					To:          "parent",
					ToColumns:   []string{"id"},
					Modify:      "from",
					ToUnique:    true,
				}},
			},
		},
		"parent": {
			{
				Name: "child_parent_fk",
				Sides: []orm.RelSide{{
					From:        "parent",
					FromColumns: []string{"id"},
					To:          "child",
					ToColumns:   []string{"parent_id"},
					Modify:      "to",
					ToUnique:    false,
				}},
			},
		},
	}
	aliases := drivers.Aliases{
		"child": {
			Relationships: map[string]string{
				"child_parent_fk": "Parent",
			},
		},
		"parent": {
			Relationships: map[string]string{
				"child_parent_fk": "Children",
			},
		},
	}

	t.Run("all is the default", func(t *testing.T) {
		t.Parallel()

		got, err := filterGeneratedRelationships(Config[any]{}, aliases, relationships)
		if err != nil {
			t.Fatal(err)
		}
		if len(got["child"]) != 1 || len(got["parent"]) != 1 {
			t.Fatalf("expected all relationships to remain, got %#v", got)
		}
	})

	t.Run("to_one drops to-many relationships", func(t *testing.T) {
		t.Parallel()

		got, err := filterGeneratedRelationships(Config[any]{
			RelationshipCodegen: RelationshipCodegen{Mode: relationshipCodegenModeToOne},
		}, aliases, relationships)
		if err != nil {
			t.Fatal(err)
		}
		if len(got["child"]) != 1 {
			t.Fatalf("expected to-one relationship to remain, got %#v", got["child"])
		}
		if len(got["parent"]) != 0 {
			t.Fatalf("expected to-many relationship to be dropped, got %#v", got["parent"])
		}
	})

	t.Run("to_one keeps allowlisted to-many relationships", func(t *testing.T) {
		t.Parallel()

		got, err := filterGeneratedRelationships(Config[any]{
			RelationshipCodegen: RelationshipCodegen{
				Mode: relationshipCodegenModeToOne,
				AllowToMany: map[string][]string{
					"parent": {"Children"},
				},
			},
		}, aliases, relationships)
		if err != nil {
			t.Fatal(err)
		}
		if len(got["child"]) != 1 || len(got["parent"]) != 1 {
			t.Fatalf("expected allowlisted to-many relationship to remain, got %#v", got)
		}
	})

	t.Run("invalid mode returns an error", func(t *testing.T) {
		t.Parallel()

		_, err := filterGeneratedRelationships(Config[any]{
			RelationshipCodegen: RelationshipCodegen{Mode: "invalid"},
		}, aliases, relationships)
		if err == nil {
			t.Fatal("expected an error")
		}
		if want := `unknown relationship_codegen.mode "invalid"`; err.Error() != want {
			t.Fatalf("got %q, want %q", err.Error(), want)
		}
	})
}
