package loaders

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/scan"
)

type loadableQuery struct{}

func (*loadableQuery) AppendLoader(...bob.Loader) {}

func (*loadableQuery) AppendMapperMod(scan.MapperMod) {}

func TestExpandLoadOptions(t *testing.T) {
	terminal := func(path string) bool { return path == "user.profile" }
	options := NewExpandLoadOptions(nil, WithMaxExpandDepth(4), WithComputedTerminal(terminal))

	if options.MaxDepth != 4 {
		t.Fatalf("expected max depth 4, got %d", options.MaxDepth)
	}
	if options.ComputedTerminal == nil || !options.ComputedTerminal("user.profile") {
		t.Fatal("expected computed terminal option to be applied")
	}
	if got := DefaultExpandLoadOptions().MaxDepth; got != 10 {
		t.Fatalf("expected default max depth 10, got %d", got)
	}
}

func TestBuildExpandTree(t *testing.T) {
	tree, err := BuildExpandTree([]string{" user.profile ", "user.videos.comments", "team"}, 3)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := tree.SortedSegments(), []string{"team", "user"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected segments: got %v, want %v", got, want)
	}
	if got, want := tree.RelativePaths(), []string{"team", "user.profile", "user.videos.comments"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected relative paths: got %v, want %v", got, want)
	}

	profile := *tree.Children["user"].Children["profile"]
	options := ExpandLoadOptions{ComputedTerminal: func(path string) bool { return path == "user.profile" }}
	if !profile.ComputedTerminal(options) {
		t.Fatal("expected profile to be a computed terminal")
	}
}

func TestBuildExpandTreeErrors(t *testing.T) {
	for _, tt := range []struct {
		name     string
		paths    []string
		maxDepth int
	}{
		{name: "depth", paths: []string{"user.profile"}, maxDepth: 1},
		{name: "empty segment", paths: []string{"user..profile"}, maxDepth: 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := BuildExpandTree(tt.paths, tt.maxDepth); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestThenLoadBuilder(t *testing.T) {
	builder := ThenLoadBuilder[*loadableQuery](
		"Profile",
		func(context.Context, bob.Executor, string, ...bob.Mod[struct{}]) error {
			return sql.ErrNoRows
		},
	)
	loader := builder()

	if err := loader(t.Context(), nil, "retrieved"); err != nil {
		t.Fatalf("expected sql.ErrNoRows to be ignored, got %v", err)
	}
	if err := loader(t.Context(), nil, 42); err == nil {
		t.Fatal("expected a type mismatch error")
	}
}
