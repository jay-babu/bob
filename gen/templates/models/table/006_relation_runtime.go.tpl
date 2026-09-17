{{$table := .Table}}
{{$tAlias := .Aliases.Table $table.Key -}}
{{if $.Relationships.UsesTable $table.Key -}}
{{$.Importer.Import "github.com/stephenafamo/bob"}}
{{$.Importer.Import (printf "github.com/stephenafamo/bob/dialect/%s/dialect" $.Dialect)}}

// {{$tAlias.UpPlural}}Relation contains lightweight metadata for generated relationships.
{{if eq $.Dialect "mysql" -}}
var {{$tAlias.UpPlural}}Relation = {{$.Dialect}}.NewRelation("{{$table.Name}}", Build{{$tAlias.UpSingular}}Columns({{quote (tableColumnAlias $table.Schema $table.Name $table.Key)}}))
{{else -}}
var {{$tAlias.UpPlural}}Relation = {{$.Dialect}}.NewRelation("{{$table.Schema}}", "{{$table.Name}}", Build{{$tAlias.UpSingular}}Columns({{quote (tableColumnAlias $table.Schema $table.Name $table.Key)}}))
{{end}}

// Query{{$tAlias.UpPlural}} starts a query without exposing the full table runtime to relationship packages.
func Query{{$tAlias.UpPlural}}(mods ...bob.Mod[*dialect.SelectQuery]) {{$tAlias.UpPlural}}Query {
	return {{$tAlias.UpPlural}}.Query(mods...)
}

{{if $table.Constraints.Primary -}}
{{$.Importer.Import "context"}}

// Insert{{$tAlias.UpSingular}} inserts and returns one model for relationship mutation helpers.
func Insert{{$tAlias.UpSingular}}(ctx context.Context, exec bob.Executor, setter *{{$tAlias.UpSingular}}Setter) (*{{$tAlias.UpSingular}}, error) {
	return {{$tAlias.UpPlural}}.Insert(setter).One(ctx, exec)
}

// Insert{{$tAlias.UpPlural}} inserts and returns models for relationship mutation helpers.
func Insert{{$tAlias.UpPlural}}(ctx context.Context, exec bob.Executor, setters ...*{{$tAlias.UpSingular}}Setter) ({{$tAlias.UpSingular}}Slice, error) {
	return {{$tAlias.UpPlural}}.Insert(bob.ToMods(setters...)).All(ctx, exec)
}
{{end -}}
{{end -}}
