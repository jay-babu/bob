{{if and $.ModelSplit $.ModelSplit.Enabled (eq $.ModelSplit.Generation "facade") -}}
{{$.Importer.Import "context"}}
{{$.Importer.Import "github.com/stephenafamo/bob"}}

{{range $table := .Tables -}}
{{$tAlias := $.Aliases.Table $table.Key -}}
{{$component := index $.ModelSplit.TableComponents $table.Key -}}
{{$.Importer.Import $component.PackagePath}}

type {{$tAlias.UpSingular}} = {{$.ModelType $table.Key}}
type {{$tAlias.UpSingular}}Slice = {{$.SliceType $table.Key}}
{{if or $table.Constraints.Primary ($.Relationships.Get $table.Key) -}}
type {{$tAlias.UpSingular}}Setter = {{$.SetterType $table.Key}}
{{end -}}
type {{$tAlias.UpPlural}}Query = {{$.QueryType $table.Key}}

var {{$tAlias.UpPlural}} = {{$.TableVar $table.Key}}

{{if $table.Constraints.Primary -}}
{{$pkArgs := ""}}
{{range $colName := $table.Constraints.Primary.Columns -}}
{{- $column := $table.GetColumn $colName -}}
{{- $colAlias := $tAlias.Column $colName -}}
{{- $colTyp := $.Types.Get $.CurrentPackage $.Importer $column.Type -}}
{{$pkArgs = printf "%s%sPK %s," $pkArgs $colAlias $colTyp}}
{{end -}}
func Find{{$tAlias.UpSingular}}(ctx context.Context, exec bob.Executor, {{$pkArgs}} cols ...string) (*{{$tAlias.UpSingular}}, error) {
	return {{$component.Package}}.Find{{$tAlias.UpSingular}}(ctx, exec, {{range $colName := $table.Constraints.Primary.Columns -}}{{- $colAlias := $tAlias.Column $colName -}}{{$colAlias}}PK, {{end}}cols...)
}

func {{$tAlias.UpSingular}}Exists(ctx context.Context, exec bob.Executor, {{$pkArgs}}) (bool, error) {
	return {{$component.Package}}.{{$tAlias.UpSingular}}Exists(ctx, exec, {{range $colName := $table.Constraints.Primary.Columns -}}{{- $colAlias := $tAlias.Column $colName -}}{{$colAlias}}PK, {{end}})
}
{{end -}}
{{end -}}
{{end -}}
