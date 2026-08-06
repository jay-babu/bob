{{$table := index .Tables 0 -}}
{{$anyRels := false -}}
{{range $t := .Tables}}{{if $.Relationships.Get $t.Key}}{{$anyRels = true}}{{end}}{{end -}}
{{if or (not $.IsTablePackage) $.IsModelSplitFacade}}{{$.Importer.Import "github.com/stephenafamo/bob/orm"}}{{end}}
{{if and $.IsModelSplitFacade $anyRels -}}
{{$.Importer.Import "fmt"}}
{{$.Importer.Import "github.com/stephenafamo/bob"}}
{{$.Importer.Import "github.com/stephenafamo/bob/orm/loaders"}}
{{$.Importer.Import (printf "github.com/stephenafamo/bob/dialect/%s" $.Dialect)}}
{{end -}}
{{if or (not $.IsTablePackage) ($.Relationships.Get $table.Key)}}{{$.Importer.Import (printf "github.com/stephenafamo/bob/dialect/%s/dialect" $.Dialect)}}{{end}}

{{if $.IsTablePackage -}}
{{if $.Relationships.Get (index .Tables 0).Key -}}
var Preload = {{$.BuildPreloaderFunc (index .Tables 0).Key}}()

var (
	SelectThenLoad = {{$.BuildThenLoaderFunc (index $.Tables 0).Key}}[*dialect.SelectQuery]()
	InsertThenLoad = {{$.BuildThenLoaderFunc (index $.Tables 0).Key}}[*dialect.InsertQuery]()
	UpdateThenLoad = {{$.BuildThenLoaderFunc (index $.Tables 0).Key}}[*dialect.UpdateQuery]()
)
{{end -}}
{{else -}}
var Preload = getPreloaders()

type preloaders struct {
		{{range $table := .Tables -}}{{if $.Relationships.Get $table.Key -}}
		{{$tAlias := $.Aliases.Table $table.Key -}}
		{{if $.IsModelSplitFacade -}}
		{{$tAlias.UpSingular}} expand{{$tAlias.UpSingular}}Preloader
		{{else -}}
		{{$tAlias.UpSingular}} {{$.PreloaderType $table.Key}}
		{{end -}}
		{{end}}{{end}}
}

func getPreloaders() preloaders {
	return preloaders{
		{{range $table := .Tables -}}{{if $.Relationships.Get $table.Key -}}
		{{$tAlias := $.Aliases.Table $table.Key -}}
		{{if $.IsModelSplitFacade -}}
		{{$tAlias.UpSingular}}: expand{{$tAlias.UpSingular}}Preloader{ {{$.BuildPreloaderFunc $table.Key}}() },
		{{else -}}
		{{$tAlias.UpSingular}}: {{$.BuildPreloaderFunc $table.Key}}(),
		{{end -}}
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
		{{if $.IsModelSplitFacade -}}
		{{$tAlias.UpSingular}} expand{{$tAlias.UpSingular}}ThenLoader[Q]
		{{else -}}
		{{$tAlias.UpSingular}} {{$.ThenLoaderType $table.Key}}[Q]
		{{end -}}
		{{end}}{{end}}
}

func getThenLoaders[Q orm.Loadable]() thenLoaders[Q] {
	return thenLoaders[Q]{
		{{range $table := .Tables -}}{{if $.Relationships.Get $table.Key -}}
		{{$tAlias := $.Aliases.Table $table.Key -}}
		{{if $.IsModelSplitFacade -}}
		{{$tAlias.UpSingular}}: expand{{$tAlias.UpSingular}}ThenLoader[Q]{ {{$.BuildThenLoaderFunc $table.Key}}[Q]() },
		{{else -}}
		{{$tAlias.UpSingular}}: {{$.BuildThenLoaderFunc $table.Key}}[Q](),
		{{end -}}
		{{end}}{{end}}
	}
}
{{end -}}

{{if $.IsModelSplitFacade -}}
{{range $table := .Tables -}}{{if $.Relationships.Get $table.Key -}}
{{$tAlias := $.Aliases.Table $table.Key -}}
type expand{{$tAlias.UpSingular}}Preloader struct {
	{{$.PreloaderType $table.Key}}
}

func (l expand{{$tAlias.UpSingular}}Preloader) ForExpandMap(expands map[string]struct{}, opts ...loaders.ExpandLoadOption) ([]bob.Mod[*dialect.SelectQuery], error) {
	paths := make([]string, 0, len(expands))
	for path := range expands {
		paths = append(paths, path)
	}

	return l.ForExpandPaths(paths, opts...)
}

func (l expand{{$tAlias.UpSingular}}Preloader) ForExpandPaths(paths []string, opts ...loaders.ExpandLoadOption) ([]bob.Mod[*dialect.SelectQuery], error) {
	options := loaders.NewExpandLoadOptions(opts...)
	tree, err := loaders.BuildExpandTree(paths, options.MaxDepth)
	if err != nil {
		return nil, err
	}

	preloadOpts, err := l.forExpandTree(tree, 0, options)
	if err != nil {
		return nil, err
	}

	mods := make([]bob.Mod[*dialect.SelectQuery], 0, len(preloadOpts))
	for _, opt := range preloadOpts {
		mod, ok := opt.(bob.Mod[*dialect.SelectQuery])
		if !ok {
			return nil, fmt.Errorf("expand preload option %T is not a select query mod", opt)
		}
		mods = append(mods, mod)
	}

	return mods, nil
}

func (l expand{{$tAlias.UpSingular}}Preloader) forExpandTree(tree loaders.ExpandTree, depth int, opts loaders.ExpandLoadOptions) ([]{{$.Dialect}}.PreloadOption, error) {
	if opts.MaxDepth >= 0 && depth > opts.MaxDepth {
		return nil, fmt.Errorf("expand path %q exceeds max depth %d", tree.Path, opts.MaxDepth)
	}

	mods := make([]{{$.Dialect}}.PreloadOption, 0, len(tree.Children))
	for _, segment := range tree.SortedSegments() {
		child := *tree.Children[segment]
		if child.ComputedTerminal(opts) {
			continue
		}

		switch segment {
		{{range $rel := $.Relationships.Get $table.Key -}}
		{{- if $rel.IsToMany -}}{{continue}}{{- end -}}
		{{- $relAlias := $tAlias.Relationship $rel.Name -}}
		{{- $fAlias := $.Aliases.Table $rel.Foreign -}}
		case {{snakecase $relAlias | quote}}:
			var childOpts []{{$.Dialect}}.PreloadOption
			{{if $.HasExpandPreloader $rel.Foreign -}}
			var err error
			childOpts, err = Preload.{{$fAlias.UpSingular}}.forExpandTree(child, depth+1, opts)
			if err != nil {
				return nil, err
			}
			{{else -}}
			if len(child.Children) > 0 {
				return nil, fmt.Errorf("expand path %q cannot be nested because {{$fAlias.UpSingular}} has no generated preload relationships", child.Path)
			}
			{{end -}}
			mods = append(mods, l.{{$relAlias}}(append(childOpts, {{$.Dialect}}.PreloadAs({{snakecase $relAlias | quote}}))...))
		{{end -}}
		default:
			return nil, fmt.Errorf("expand segment %q does not match a relationship on {{$tAlias.UpSingular}}", segment)
		}
	}

	return mods, nil
}

type expand{{$tAlias.UpSingular}}ThenLoader[Q orm.Loadable] struct {
	{{$.ThenLoaderType $table.Key}}[Q]
}

func (l expand{{$tAlias.UpSingular}}ThenLoader[Q]) ForExpandMap(expands map[string]struct{}, opts ...loaders.ExpandLoadOption) ([]bob.Mod[Q], error) {
	paths := make([]string, 0, len(expands))
	for path := range expands {
		paths = append(paths, path)
	}

	return l.ForExpandPaths(paths, opts...)
}

func (l expand{{$tAlias.UpSingular}}ThenLoader[Q]) ForExpandPaths(paths []string, opts ...loaders.ExpandLoadOption) ([]bob.Mod[Q], error) {
	options := loaders.NewExpandLoadOptions(opts...)
	tree, err := loaders.BuildExpandTree(paths, options.MaxDepth)
	if err != nil {
		return nil, err
	}

	return l.forExpandTree(tree, 0, options)
}

func (l expand{{$tAlias.UpSingular}}ThenLoader[Q]) forExpandTree(tree loaders.ExpandTree, depth int, opts loaders.ExpandLoadOptions) ([]bob.Mod[Q], error) {
	if opts.MaxDepth >= 0 && depth > opts.MaxDepth {
		return nil, fmt.Errorf("expand path %q exceeds max depth %d", tree.Path, opts.MaxDepth)
	}

	mods := make([]bob.Mod[Q], 0, len(tree.Children))
	for _, segment := range tree.SortedSegments() {
		child := *tree.Children[segment]
		if child.ComputedTerminal(opts) {
			continue
		}

		switch segment {
		{{range $rel := $.Relationships.Get $table.Key -}}
		{{- $relAlias := $tAlias.Relationship $rel.Name -}}
		{{- $fAlias := $.Aliases.Table $rel.Foreign -}}
		case {{snakecase $relAlias | quote}}:
			{{if $.HasExpandThenLoader $rel.Foreign -}}
			childMods, err := SelectThenLoad.{{$fAlias.UpSingular}}.forExpandTree(child, depth+1, opts)
			if err != nil {
				return nil, err
			}
			mods = append(mods, l.{{$relAlias}}(childMods...))
			{{else -}}
			if len(child.Children) > 0 {
				return nil, fmt.Errorf("expand path %q cannot be nested because {{$fAlias.UpSingular}} has no generated expand relationships", child.Path)
			}
			mods = append(mods, l.{{$relAlias}}())
			{{end -}}
		{{end -}}
		default:
			return nil, fmt.Errorf("expand segment %q does not match a relationship on {{$tAlias.UpSingular}}", segment)
		}
	}

	return mods, nil
}

{{end}}{{end -}}
{{end -}}
