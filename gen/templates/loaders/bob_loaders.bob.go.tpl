{{$.Importer.Import "fmt"}}
{{$.Importer.Import "errors"}}
{{$.Importer.Import "context"}}
{{$.Importer.Import "database/sql"}}
{{$.Importer.Import "sort"}}
{{$.Importer.Import "strings"}}
{{$.Importer.Import "github.com/stephenafamo/bob"}}
{{$.Importer.Import "github.com/stephenafamo/bob/orm"}}
{{$.Importer.Import (printf "github.com/stephenafamo/bob/dialect/%s/dialect" $.Dialect)}}

var Preload = getPreloaders()

type preloaders struct {
		{{range $table := .Tables -}}{{if $.Relationships.Get $table.Key -}}
		{{$tAlias := $.Aliases.Table $table.Key -}}
		{{$tAlias.UpSingular}} {{$.PreloaderType $table.Key}}
		{{end}}{{end}}
}

func getPreloaders() preloaders {
	return preloaders{
		{{range $table := .Tables -}}{{if $.Relationships.Get $table.Key -}}
		{{$tAlias := $.Aliases.Table $table.Key -}}
		{{$tAlias.UpSingular}}: {{$.BuildPreloaderFunc $table.Key}}(),
		{{end}}{{end}}
	}
}

{{block "helpers/then_load_variables" . -}}
var (
	SelectThenLoad = getThenLoaders[*dialect.SelectQuery]()
	InsertThenLoad = getThenLoaders[*dialect.InsertQuery]()
	UpdateThenLoad = getThenLoaders[*dialect.UpdateQuery]()
)
{{- end}}

type thenLoaders[Q orm.Loadable] struct {
		{{range $table := .Tables -}}{{if $.Relationships.Get $table.Key -}}
		{{$tAlias := $.Aliases.Table $table.Key -}}
		{{$tAlias.UpSingular}} {{$.ThenLoaderType $table.Key}}[Q]
		{{end}}{{end}}
}

func getThenLoaders[Q orm.Loadable]() thenLoaders[Q] {
	return thenLoaders[Q]{
		{{range $table := .Tables -}}{{if $.Relationships.Get $table.Key -}}
		{{$tAlias := $.Aliases.Table $table.Key -}}
		{{$tAlias.UpSingular}}: {{$.BuildThenLoaderFunc $table.Key}}[Q](),
		{{end}}{{end}}
	}
}


func thenLoadBuilder[Q orm.Loadable, T any](name string, f func(context.Context, bob.Executor, T, ...bob.Mod[*dialect.SelectQuery]) error) func(...bob.Mod[*dialect.SelectQuery]) orm.Loader[Q] {
	return func(queryMods ...bob.Mod[*dialect.SelectQuery]) orm.Loader[Q] {
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

type ExpandLoadOption func(*expandLoadOptions)

type expandLoadOptions struct {
	maxDepth         int
	computedTerminal func(path string) bool
}

func defaultExpandLoadOptions() expandLoadOptions {
	return expandLoadOptions{maxDepth: 10}
}

func newExpandLoadOptions(opts ...ExpandLoadOption) expandLoadOptions {
	options := defaultExpandLoadOptions()
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}

	return options
}

func WithMaxExpandDepth(depth int) ExpandLoadOption {
	return func(options *expandLoadOptions) {
		options.maxDepth = depth
	}
}

func WithComputedTerminal(fn func(path string) bool) ExpandLoadOption {
	return func(options *expandLoadOptions) {
		options.computedTerminal = fn
	}
}

type expandTree struct {
	path     string
	children map[string]*expandTree
}

func buildExpandTree(paths []string, maxDepth int) (expandTree, error) {
	root := expandTree{children: map[string]*expandTree{}}
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}

		segments := strings.Split(path, ".")
		if maxDepth >= 0 && len(segments) > maxDepth {
			return expandTree{}, fmt.Errorf("expand path %q exceeds max depth %d", path, maxDepth)
		}

		node := &root
		currentPath := ""
		for _, segment := range segments {
			segment = strings.TrimSpace(segment)
			if segment == "" {
				return expandTree{}, fmt.Errorf("expand path %q contains an empty segment", path)
			}

			if currentPath == "" {
				currentPath = segment
			} else {
				currentPath += "." + segment
			}

			if node.children == nil {
				node.children = map[string]*expandTree{}
			}

			child := node.children[segment]
			if child == nil {
				child = &expandTree{path: currentPath, children: map[string]*expandTree{}}
				node.children[segment] = child
			}

			node = child
		}
	}

	return root, nil
}

func (tree expandTree) sortedSegments() []string {
	segments := make([]string, 0, len(tree.children))
	for segment := range tree.children {
		segments = append(segments, segment)
	}
	sort.Strings(segments)

	return segments
}

func (tree expandTree) computedTerminal(options expandLoadOptions) bool {
	return len(tree.children) == 0 && options.computedTerminal != nil && options.computedTerminal(tree.path)
}
