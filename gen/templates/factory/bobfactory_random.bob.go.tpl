{{- $isSplit := and $.ModelSplit $.ModelSplit.Enabled -}}
{{- $isFacade := and $isSplit (eq $.ModelSplit.Generation "facade") -}}
{{- if not $isFacade -}}
{{$.Importer.Import "github.com/jaswdr/faker/v2"}}


var defaultFaker = faker.New()

{{$doneTypes := dict }}
{{- range $table := .Tables}}
{{- $tAlias := $.Aliases.Table $table.Key}}
  {{range $column := $table.Columns -}}
    {{range $depTyp := $.Types.DependencyClosure $column.Type}}
      {{- $_ := set $doneTypes $depTyp nil -}}
    {{end}}
  {{end -}}
{{- end}}


{{range $colTyp := keys $doneTypes | sortAlpha -}}
    {{- $typDef := $.Types.Index $colTyp -}}
    {{- if not $typDef.RandomExpr -}}{{continue}}{{/*
      Ensures that compilation fails.
      Users of custom types can decide to use a non-random expression
      but this would be a conscious decision.
    */}}{{- end -}}
    {{- $typ := $.Types.Get $.CurrentPackage $.Importer $colTyp -}}
    {{- $.Importer.ImportList $typDef.RandomExprImports -}}
    func random_{{normalizeType $colTyp}}(f *faker.Faker, limits ...string) {{$typ}} {
      if f == nil {
        f = &defaultFaker
      }

      {{replace "BASETYPE" $typ $typDef.RandomExpr}}
    }
{{end -}}
{{end}}
