package loaders

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/orm"
)

func ThenLoadBuilder[Q orm.Loadable, T any, S any](name string, f func(context.Context, bob.Executor, T, ...bob.Mod[S]) error) func(...bob.Mod[S]) orm.Loader[Q] {
	return func(queryMods ...bob.Mod[S]) orm.Loader[Q] {
		return func(ctx context.Context, exec bob.Executor, retrieved any) error {
			loader, isLoader := retrieved.(T)
			if !isLoader {
				return fmt.Errorf("object %T cannot load %q", retrieved, name)
			}

			err := f(ctx, exec, loader, queryMods...)

			// Don't cause an issue due to missing relationships
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}

			return err
		}
	}
}

type ExpandLoadOption func(*ExpandLoadOptions)

type ExpandLoadOptions struct {
	MaxDepth         int
	ComputedTerminal func(path string) bool
}

func DefaultExpandLoadOptions() ExpandLoadOptions {
	return ExpandLoadOptions{MaxDepth: 10}
}

func NewExpandLoadOptions(opts ...ExpandLoadOption) ExpandLoadOptions {
	options := DefaultExpandLoadOptions()
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}

	return options
}

func WithMaxExpandDepth(depth int) ExpandLoadOption {
	return func(options *ExpandLoadOptions) {
		options.MaxDepth = depth
	}
}

func WithComputedTerminal(fn func(path string) bool) ExpandLoadOption {
	return func(options *ExpandLoadOptions) {
		options.ComputedTerminal = fn
	}
}

type ExpandTree struct {
	Path     string
	Children map[string]*ExpandTree
}

func BuildExpandTree(paths []string, maxDepth int) (ExpandTree, error) {
	root := ExpandTree{Children: map[string]*ExpandTree{}}
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}

		segments := strings.Split(path, ".")
		if maxDepth >= 0 && len(segments) > maxDepth {
			return ExpandTree{}, fmt.Errorf("expand path %q exceeds max depth %d", path, maxDepth)
		}

		node := &root
		currentPath := ""
		for _, segment := range segments {
			segment = strings.TrimSpace(segment)
			if segment == "" {
				return ExpandTree{}, fmt.Errorf("expand path %q contains an empty segment", path)
			}

			if currentPath == "" {
				currentPath = segment
			} else {
				currentPath += "." + segment
			}

			if node.Children == nil {
				node.Children = map[string]*ExpandTree{}
			}

			child := node.Children[segment]
			if child == nil {
				child = &ExpandTree{Path: currentPath, Children: map[string]*ExpandTree{}}
				node.Children[segment] = child
			}

			node = child
		}
	}

	return root, nil
}

func (tree ExpandTree) SortedSegments() []string {
	segments := make([]string, 0, len(tree.Children))
	for segment := range tree.Children {
		segments = append(segments, segment)
	}
	sort.Strings(segments)

	return segments
}

func (tree ExpandTree) RelativePaths() []string {
	paths := make([]string, 0)
	var walk func(ExpandTree, string)
	walk = func(node ExpandTree, prefix string) {
		for _, segment := range node.SortedSegments() {
			child := *node.Children[segment]
			path := segment
			if prefix != "" {
				path = prefix + "." + segment
			}
			if len(child.Children) == 0 {
				paths = append(paths, path)
				continue
			}
			walk(child, path)
		}
	}
	walk(tree, "")
	return paths
}

func (tree ExpandTree) ComputedTerminal(options ExpandLoadOptions) bool {
	return len(tree.Children) == 0 && options.ComputedTerminal != nil && options.ComputedTerminal(tree.Path)
}
