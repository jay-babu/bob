package gen

import (
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"text/template"

	"github.com/stephenafamo/bob/gen/drivers"
	"github.com/stephenafamo/bob/orm"
)

func TestGenerateSplitFactoryOutputGeneratesShallowAndRelationshipVariants(t *testing.T) {
	t.Parallel()

	output, data := splitFactoryTestFixture(t, BaseTemplates.Factory)
	staleRelationshipDir := filepath.Join(output.OutFolder, "public", "child", "relationships")
	for file, contents := range map[string]string{
		filepath.Join(staleRelationshipDir, "stale.bob.go"): "generated",
		filepath.Join(staleRelationshipDir, "custom.go"):    "handwritten",
	} {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	originalTables := data.Tables
	originalRelationships := data.Relationships
	originalModelSplit := data.ModelSplit
	originalModelsPackage := data.OutputPackages["models"]
	originalPkgName := data.PkgName
	originalTable := data.Table
	originalCurrentPackage := data.CurrentPackage
	originalImporter := data.Importer
	originalLanguage := data.Language

	if err := generateSplitFactoryOutput(&output, &data, "BobGen", true); err != nil {
		t.Fatal(err)
	}
	assertSplitFactoryTemplateState(t, data, originalTables, originalRelationships, originalModelSplit,
		originalModelsPackage, originalPkgName, originalTable, originalCurrentPackage, originalImporter, originalLanguage)

	shallow := readTestFile(t, filepath.Join(output.OutFolder, "public", "child", "public.child.bob.go"))
	for _, want := range []string{
		"func NewChildWithContext",
		"func FromExistingChild",
		"func (o ChildTemplate) Build()",
		"func (o *ChildTemplate) Create(",
		"func (o ChildTemplate) CreateMany(",
		"func (m childMods) RandomizeAllColumns(",
		`models "example.com/_factorymodels/public/child"`,
	} {
		if !strings.Contains(shallow, want) {
			t.Fatalf("shallow factory missing %q:\n%s", want, shallow)
		}
	}
	for _, unwanted := range []string{
		"WithParent",
		"childR struct",
		"example.com/_factory_test/public/parent",
		"example.com/foreign",
	} {
		if strings.Contains(shallow, unwanted) {
			t.Fatalf("shallow factory contains relationship/foreign dependency %q:\n%s", unwanted, shallow)
		}
	}

	shallowModels := readTestFile(t, filepath.Join(
		filepath.Dir(data.ModelSplit.RootOutFolder),
		"_factorymodels", "public", "child", "bob_factory_models.bob.go",
	))
	if !strings.Contains(shallowModels, `child "example.com/bobmodels/public/child"`) {
		t.Fatalf("shallow model facade is missing its own model:\n%s", shallowModels)
	}
	if strings.Contains(shallowModels, "example.com/bobmodels/public/parent") {
		t.Fatalf("shallow model facade imports a foreign model:\n%s", shallowModels)
	}

	relationship := readTestFile(t, filepath.Join(
		output.OutFolder, "public", "child", "relationships", "public.child.bob.go",
	))
	for _, want := range []string{
		"func NewChildWithContext",
		"func FromExistingChild",
		"WithParent",
		"example.com/_factory_test/public/parent/relationships",
		`models "example.com/_factorymodels/public/child/relationships"`,
	} {
		if !strings.Contains(relationship, want) {
			t.Fatalf("relationship factory missing %q:\n%s", want, relationship)
		}
	}
	for _, unwanted := range []string{
		"type ChildTemplate = ChildTemplate",
		"var ChildMods = ChildMods",
	} {
		if strings.Contains(relationship, unwanted) {
			t.Fatalf("relationship factory contains recursive self alias %q:\n%s", unwanted, relationship)
		}
	}

	relationshipModels := readTestFile(t, filepath.Join(
		filepath.Dir(data.ModelSplit.RootOutFolder),
		"_factorymodels", "public", "child", "relationships", "bob_factory_models.bob.go",
	))
	for _, want := range []string{
		`child "example.com/bobmodels/public/child"`,
		`parent "example.com/bobmodels/public/parent"`,
	} {
		if !strings.Contains(relationshipModels, want) {
			t.Fatalf("relationship model facade missing closure import %q:\n%s", want, relationshipModels)
		}
	}

	if _, err := os.Stat(filepath.Join(staleRelationshipDir, "stale.bob.go")); !os.IsNotExist(err) {
		t.Fatalf("stale nested generated file still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(staleRelationshipDir, "custom.go")); err != nil {
		t.Fatalf("nested handwritten file was removed: %v", err)
	}
}

func TestGenerateSplitFactoryOutputRestoresTemplateStateAfterError(t *testing.T) {
	t.Parallel()

	templates := fstest.MapFS{
		"marker.bob.go.tpl": &fstest.MapFile{Data: []byte("const Generated = true\n")},
		"table/fail.go.tpl": &fstest.MapFile{Data: []byte(`
{{if $.Relationships.Get .Table.Key}}{{fail "forced relationship generation failure"}}{{end}}
const TableGenerated = true
`)},
	}
	output, data := splitFactoryTestFixture(t, templates)
	originalTables := data.Tables
	originalRelationships := data.Relationships
	originalModelSplit := data.ModelSplit
	originalModelsPackage := data.OutputPackages["models"]
	originalPkgName := data.PkgName
	originalTable := data.Table
	originalCurrentPackage := data.CurrentPackage
	originalImporter := data.Importer
	originalLanguage := data.Language

	err := generateSplitFactoryOutput(&output, &data, "BobGen", true)
	if err == nil || !strings.Contains(err.Error(), "forced relationship generation failure") {
		t.Fatalf("expected forced relationship generation failure, got %v", err)
	}
	assertSplitFactoryTemplateState(t, data, originalTables, originalRelationships, originalModelSplit,
		originalModelsPackage, originalPkgName, originalTable, originalCurrentPackage, originalImporter, originalLanguage)
}

func splitFactoryTestFixture(t *testing.T, factoryTemplates fs.FS) (Output, TemplateData[any, any, any]) {
	t.Helper()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	modelsFolder := filepath.Join(root, "bobmodels")
	factoryFolder := filepath.Join(root, "_factory_test")
	tables := drivers.Tables[any, any]{
		{
			Key:    "public.child",
			Schema: "public",
			Name:   "child",
			Columns: []drivers.Column{
				{Name: "id", Type: "string"},
				{Name: "parent_id", Type: "string"},
			},
			Constraints: drivers.Constraints[any]{
				Primary: &drivers.Constraint[any]{Name: "child_pkey", Columns: []string{"id"}},
				Foreign: []drivers.ForeignKey[any]{{
					Constraint:     drivers.Constraint[any]{Name: "child_parent_fk", Columns: []string{"parent_id"}},
					ForeignTable:   "public.parent",
					ForeignColumns: []string{"id"},
				}},
			},
		},
		{
			Key:    "public.parent",
			Schema: "public",
			Name:   "parent",
			Columns: []drivers.Column{
				{Name: "id", Type: "string"},
				{Name: "foreign_only", Type: "foreign.Type"},
			},
			Constraints: drivers.Constraints[any]{
				Primary: &drivers.Constraint[any]{Name: "parent_pkey", Columns: []string{"id"}},
			},
		},
	}
	relationships := buildRelationships(tables)
	if err := initRelationships(relationships, tables); err != nil {
		t.Fatal(err)
	}
	relationships = prepareTablePackageRelationships(relationships)

	types := drivers.Types{}
	types.SetTypeModifier(drivers.AarondlNull{})
	types.Register("string", drivers.Type{RandomExpr: `return "value"`})
	types.Register("foreign.Type", drivers.Type{
		Imports:    []string{`foreign "example.com/foreign"`},
		RandomExpr: "return foreign.New()",
	})

	modelSplit := buildModelSplitData(modelsFolder, "example.com/bobmodels", tables)
	data := TemplateData[any, any, any]{
		Table:         drivers.Table[any, any]{Key: "sentinel"},
		Tables:        tables,
		AllTables:     tables,
		Types:         types,
		Relationships: relationships,
		Aliases: drivers.Aliases{
			"public.child": {
				UpPlural:      "Children",
				UpSingular:    "Child",
				DownPlural:    "children",
				DownSingular:  "child",
				Columns:       map[string]string{"id": "ID", "parent_id": "ParentID"},
				Relationships: map[string]string{"child_parent_fk": "Parent"},
			},
			"public.parent": {
				UpPlural:      "Parents",
				UpSingular:    "Parent",
				DownPlural:    "parents",
				DownSingular:  "parent",
				Columns:       map[string]string{"id": "ID", "foreign_only": "ForeignOnly"},
				Relationships: map[string]string{"child_parent_fk": "Children"},
			},
		},
		PkgName:            "sentinelpkg",
		CurrentPackage:     "example.com/sentinel",
		OutputPackages:     map[string]string{"models": "example.com/bobmodels", "factory": "example.com/_factory_test"},
		ModelSplit:         modelSplit,
		RelationLoadedName: "Loaded",
		Driver:             "database/sql",
	}
	output := Output{
		Key:       "factory",
		PkgName:   "factory_test",
		OutFolder: factoryFolder,
		Templates: []fs.FS{factoryTemplates},
	}
	if err := output.initTemplates(nil); err != nil {
		t.Fatal(err)
	}

	return output, data
}

func assertSplitFactoryTemplateState(
	t *testing.T,
	data TemplateData[any, any, any],
	tables drivers.Tables[any, any],
	relationships Relationships,
	modelSplit *ModelSplitData,
	modelsPackage string,
	pkgName string,
	table drivers.Table[any, any],
	currentPackage string,
	importer any,
	language any,
) {
	t.Helper()

	if !reflect.DeepEqual(data.Tables, tables) || !reflect.DeepEqual(data.Relationships, relationships) {
		t.Fatal("split factory generation did not restore tables and relationships")
	}
	if data.ModelSplit != modelSplit || data.OutputPackages["models"] != modelsPackage {
		t.Fatal("split factory generation did not restore split/package state")
	}
	if data.PkgName != pkgName || !reflect.DeepEqual(data.Table, table) || data.CurrentPackage != currentPackage {
		t.Fatal("split factory generation did not restore current template state")
	}
	if !reflect.DeepEqual(data.Importer, importer) || !reflect.DeepEqual(data.Language, language) {
		t.Fatal("split factory generation did not restore language state")
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func TestCleanGeneratedSubdirectoriesPreservesHandwrittenFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stale := filepath.Join(root, "stale")
	generatedOnly := filepath.Join(root, "generatedonly")
	for _, dir := range []string{stale, generatedOnly} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for file, contents := range map[string]string{
		filepath.Join(root, "bob_facade.bob.go"):     "generated",
		filepath.Join(root, "custom.go"):             "handwritten",
		filepath.Join(stale, "model.bob.go"):         "generated",
		filepath.Join(stale, "custom.go"):            "handwritten",
		filepath.Join(generatedOnly, "model.bob.go"): "generated",
	} {
		if err := os.WriteFile(file, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := cleanGeneratedSubdirectories(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "bob_facade.bob.go")); !os.IsNotExist(err) {
		t.Fatalf("root generated file still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "custom.go")); err != nil {
		t.Fatalf("root handwritten file removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stale, "model.bob.go")); !os.IsNotExist(err) {
		t.Fatalf("generated file still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stale, "custom.go")); err != nil {
		t.Fatalf("handwritten file removed: %v", err)
	}
	if _, err := os.Stat(generatedOnly); !os.IsNotExist(err) {
		t.Fatalf("empty generated directory still exists: %v", err)
	}
}

func TestModelSplitDoesNotGenerateFacade(t *testing.T) {
	t.Parallel()

	if (&ModelSplitData{}).GeneratesFacade() {
		t.Fatal("schema/table packages must not generate a root facade")
	}
}

func TestGenerateSplitFactoryOutputDoesNotGenerateRootPackage(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com\n\n go 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	modelsFolder := filepath.Join(root, "bobmodels")
	factoryFolder := filepath.Join(root, "_factory_test")
	factoryModelsFolder := filepath.Join(root, "_factorymodels")
	if err := os.MkdirAll(factoryModelsFolder, 0o755); err != nil {
		t.Fatal(err)
	}
	for file, contents := range map[string]string{
		filepath.Join(factoryFolder, "stale_root.bob.go"):                           "generated",
		filepath.Join(factoryFolder, "custom.go"):                                   "handwritten",
		filepath.Join(factoryModelsFolder, "bob_factory_models.bob.go"):             "generated",
		filepath.Join(factoryModelsFolder, "custom.go"):                             "handwritten",
		filepath.Join(factoryModelsFolder, "public", "entity", "custom_helpers.go"): "handwritten component helper",
	} {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tables := drivers.Tables[any, any]{{Key: "public.entity", Schema: "public", Name: "entity"}}
	modelSplit := buildModelSplitData(modelsFolder, "example.com/bobmodels", tables)
	data := TemplateData[any, any, any]{
		Tables:        tables,
		AllTables:     tables,
		Aliases:       drivers.Aliases{"public.entity": {UpPlural: "Entities", UpSingular: "Entity"}},
		Relationships: Relationships{},
		OutputPackages: map[string]string{
			"models":  "example.com/bobmodels",
			"factory": "example.com/_factory_test",
		},
		ModelSplit: modelSplit,
	}
	output := Output{
		Key:                "factory",
		PkgName:            "factory_test",
		OutFolder:          factoryFolder,
		singletonTemplates: template.Must(template.New("marker.bob.go.tpl").Parse("const GeneratedFor = {{printf \"%q\" .ModelSplit.Generation}}\n")),
		tableTemplates:     template.New(""),
		queryTemplates:     template.New(""),
	}

	if err := generateSplitFactoryOutput(&output, &data, "BobGen", true); err != nil {
		t.Fatal(err)
	}

	for _, file := range []string{
		filepath.Join(factoryFolder, "marker.bob.go"),
		filepath.Join(factoryFolder, "stale_root.bob.go"),
		filepath.Join(factoryModelsFolder, "bob_factory_models.bob.go"),
	} {
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Fatalf("root generated file still exists: %s: %v", file, err)
		}
	}
	for _, file := range []string{
		filepath.Join(factoryFolder, "custom.go"),
		filepath.Join(factoryModelsFolder, "custom.go"),
		filepath.Join(factoryModelsFolder, "public", "entity", "custom_helpers.go"),
		filepath.Join(factoryFolder, "public", "entity", "marker.bob.go"),
		filepath.Join(factoryModelsFolder, "public", "entity", "bob_factory_models.bob.go"),
	} {
		if _, err := os.Stat(file); err != nil {
			t.Fatalf("expected file missing: %s: %v", file, err)
		}
	}
}

func TestFactoryPackageUsesScopedTablePackage(t *testing.T) {
	t.Parallel()

	tables := drivers.Tables[any, any]{{Key: "public.entity", Name: "entity"}}
	data := TemplateData[any, any, any]{
		OutputPackages: map[string]string{"factory": "example.com/_factory_test"},
		ModelSplit:     buildModelSplitData("/tmp/models", "example.com/models", tables),
	}

	if got, want := data.FactoryPackage("public.entity"), "example.com/_factory_test/public/entity"; got != want {
		t.Fatalf("factory package: want %q, got %q", want, got)
	}
}

func TestFactoryRelationshipsPackageUsesOptInPackage(t *testing.T) {
	t.Parallel()

	tables := drivers.Tables[any, any]{{Key: "public.entity", Name: "entity"}}
	data := TemplateData[any, any, any]{
		OutputPackages: map[string]string{"factory": "example.com/_factory_test"},
		ModelSplit:     buildModelSplitData("/tmp/models", "example.com/models", tables),
	}

	if got, want := data.FactoryRelationshipsPackage("public.entity"), "example.com/_factory_test/public/entity/relationships"; got != want {
		t.Fatalf("relationship factory package: want %q, got %q", want, got)
	}
	if got, want := data.FactoryRelationshipsPackage("public.unknown"), "example.com/_factory_test"; got != want {
		t.Fatalf("unknown relationship factory package: want %q, got %q", want, got)
	}

	data.ModelSplit = nil
	if got, want := data.FactoryRelationshipsPackage("public.entity"), "example.com/_factory_test"; got != want {
		t.Fatalf("non-split relationship factory package: want %q, got %q", want, got)
	}
}

func TestBuildModelSplitDataTablePackages(t *testing.T) {
	t.Parallel()

	tables := drivers.Tables[any, any]{
		{Key: "public.entity", Name: "entity"},
		{Key: "public.loyalty_program", Name: "loyalty_program"},
	}

	got := buildModelSplitData(
		"/tmp/models",
		"example.com/models",
		tables,
	)

	if len(got.Components) != 2 {
		t.Fatalf("expected one component per table, got %d", len(got.Components))
	}

	entity := got.TableComponents["public.entity"]
	if entity.Package != "entity" {
		t.Fatalf("entity package: want entity, got %q", entity.Package)
	}
	if entity.OutFolder != filepath.Join("/tmp/models", "public", "entity") {
		t.Fatalf("entity output: want /tmp/models/public/entity, got %q", entity.OutFolder)
	}
	if entity.PackagePath != "example.com/models/public/entity" {
		t.Fatalf("entity import path: want example.com/models/public/entity, got %q", entity.PackagePath)
	}

	loyaltyProgram := got.TableComponents["public.loyalty_program"]
	if loyaltyProgram.Package != "loyaltyprogram" {
		t.Fatalf("loyalty program package: want loyaltyprogram, got %q", loyaltyProgram.Package)
	}
	if loyaltyProgram.ImportAlias != "loyaltyprogram" {
		t.Fatalf("loyalty program import alias: want loyaltyprogram, got %q", loyaltyProgram.ImportAlias)
	}
	if loyaltyProgram.RelativePath != "public/loyalty_program" {
		t.Fatalf("loyalty program relative path: want public/loyalty_program, got %q", loyaltyProgram.RelativePath)
	}
	if loyaltyProgram.PackagePath != "example.com/models/public/loyalty_program" {
		t.Fatalf("loyalty program import path: want example.com/models/public/loyalty_program, got %q", loyaltyProgram.PackagePath)
	}
}

func TestGenerateFactoryModelsFacadeImportsOnlySelectedComponents(t *testing.T) {
	t.Parallel()

	allTables := drivers.Tables[any, any]{
		{Key: "public.entity", Name: "entity"},
		{Key: "public.loyalty_program", Name: "loyalty_program"},
	}
	modelSplit := buildModelSplitData(
		"/tmp/models",
		"example.com/models",
		allTables,
	)
	data := TemplateData[any, any, any]{
		Aliases: drivers.Aliases{
			"public.entity": {
				UpPlural:   "Entities",
				UpSingular: "Entity",
			},
			"public.loyalty_program": {
				UpPlural:   "LoyaltyPrograms",
				UpSingular: "LoyaltyProgram",
			},
		},
		Relationships: Relationships{},
	}

	outFolder := t.TempDir()
	if err := generateFactoryModelsFacade(outFolder, modelSplit, allTables[:1], &data); err != nil {
		t.Fatal(err)
	}

	generated, err := os.ReadFile(filepath.Join(outFolder, "bob_factory_models.bob.go"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(generated)
	for _, want := range []string{
		`entity "example.com/models/public/entity"`,
		"type Entity = entity.Entity",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected scoped factory model facade to contain %q, got:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{
		"example.com/models/public/loyalty_program",
		"type LoyaltyProgram =",
	} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("scoped factory model facade contains unrelated model %q:\n%s", unwanted, got)
		}
	}
}

func TestGenerateComponentFactoryModelsFacadeScopesRelationshipClosure(t *testing.T) {
	t.Parallel()

	allTables := drivers.Tables[any, any]{
		{Key: "public.entity", Name: "entity"},
		{Key: "public.loyalty_program", Name: "loyalty_program"},
		{Key: "audit.audit_log", Name: "audit_log"},
	}
	modelSplit := buildModelSplitData(
		"/tmp/models",
		"example.com/models",
		allTables,
	)
	data := TemplateData[any, any, any]{
		AllTables: allTables,
		Aliases: drivers.Aliases{
			"public.entity": {
				UpPlural:   "Entities",
				UpSingular: "Entity",
			},
			"public.loyalty_program": {
				UpPlural:   "LoyaltyPrograms",
				UpSingular: "LoyaltyProgram",
			},
			"audit.audit_log": {
				UpPlural:   "AuditLogs",
				UpSingular: "AuditLog",
			},
		},
		Relationships: Relationships{
			"public.entity": {{
				Name: "entity_loyalty_program_fk",
				Sides: []orm.RelSide{{
					From:   "public.entity",
					To:     "public.loyalty_program",
					Modify: "from",
				}},
			}},
		},
		ModelSplit: modelSplit,
	}

	facadesFolder := t.TempDir()
	facadesPackage := "example.com/_factorymodels"
	component := modelSplit.TableComponents["public.entity"]
	gotPackage, err := generateComponentFactoryModelsFacade(
		facadesFolder,
		facadesPackage,
		modelSplit,
		modelSplit,
		component,
		allTables,
		&data,
	)
	if err != nil {
		t.Fatal(err)
	}
	wantPackage := "example.com/_factorymodels/public/entity"
	if gotPackage != wantPackage {
		t.Fatalf("component facade package: want %q, got %q", wantPackage, gotPackage)
	}

	generated, err := os.ReadFile(filepath.Join(
		facadesFolder,
		"public",
		"entity",
		"bob_factory_models.bob.go",
	))
	if err != nil {
		t.Fatal(err)
	}
	got := string(generated)
	for _, want := range []string{
		`entity "example.com/models/public/entity"`,
		`loyaltyprogram "example.com/models/public/loyalty_program"`,
		"type Entity = entity.Entity",
		"type LoyaltyProgram = loyaltyprogram.LoyaltyProgram",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected component factory model facade to contain %q, got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "example.com/models/audit/audit_log") {
		t.Fatalf("component factory model facade imports unrelated audit model:\n%s", got)
	}
}

func TestBuildModelSplitDataSanitizesPackageNamesAndDisambiguatesAliases(t *testing.T) {
	t.Parallel()

	got := buildModelSplitData(
		"/tmp/models",
		"example.com/models",
		drivers.Tables[any, any]{
			{Key: "sales.order-item", Name: "order-item"},
			{Key: "sales.type", Name: "type"},
			{Key: "a_b.c", Name: "c"},
			{Key: "a.bc", Name: "bc"},
			{Key: "t.ype", Name: "ype"},
		},
	)

	orderItem := got.TableComponents["sales.order-item"]
	if orderItem.Package == "order-item" || orderItem.RelativePath == "sales/order-item" {
		t.Fatalf("unsafe SQL identifier was used as a Go package: %#v", orderItem)
	}
	if orderItem.PackagePath != "example.com/models/"+orderItem.RelativePath {
		t.Fatalf("package path and relative path disagree: %#v", orderItem)
	}

	keyword := got.TableComponents["sales.type"]
	if keyword.Package == "type" {
		t.Fatalf("Go keyword was used as a package name: %#v", keyword)
	}

	left := got.TableComponents["a_b.c"]
	right := got.TableComponents["a.bc"]
	if left.ImportAlias == right.ImportAlias {
		t.Fatalf("colliding table keys produced duplicate import alias %q", left.ImportAlias)
	}

	keywordAlias := got.TableComponents["t.ype"]
	if !token.IsIdentifier(keywordAlias.ImportAlias) {
		t.Fatalf("schema/table concatenation produced invalid import alias %q", keywordAlias.ImportAlias)
	}
}

func TestBuildModelSplitDataDisambiguatesSameTableAcrossSchemas(t *testing.T) {
	t.Parallel()

	got := buildModelSplitData(
		"/tmp/models",
		"example.com/models",
		drivers.Tables[any, any]{
			{Key: "public.widget", Name: "widget"},
			{Key: "reference.widget", Name: "widget"},
		},
	)

	public := got.TableComponents["public.widget"]
	reference := got.TableComponents["reference.widget"]
	if public.PackagePath != "example.com/models/public/widget" || public.ImportAlias != "publicwidget" {
		t.Fatalf("unexpected public widget component: %#v", public)
	}
	if reference.PackagePath != "example.com/models/reference/widget" || reference.ImportAlias != "referencewidget" {
		t.Fatalf("unexpected reference widget component: %#v", reference)
	}
}

func TestPrepareTablePackageRelationshipsDropsReverseToMany(t *testing.T) {
	t.Parallel()

	childToParent := orm.Relationship{
		Name:  "child_parent_fk",
		Sides: []orm.RelSide{{From: "child", To: "parent", Modify: "from", ToUnique: true}},
	}
	parentToChildren := orm.Relationship{
		Name:  "child_parent_fk",
		Sides: []orm.RelSide{{From: "parent", To: "child", Modify: "to", ToUnique: false}},
	}

	got := prepareTablePackageRelationships(Relationships{
		"child":  {childToParent},
		"parent": {parentToChildren},
	})
	if len(got["child"]) != 1 {
		t.Fatalf("expected child-to-parent relationship, got %#v", got["child"])
	}
	if len(got["parent"]) != 0 {
		t.Fatalf("expected reverse parent-to-child relationship to be dropped, got %#v", got["parent"])
	}
}

func TestPrepareTablePackageRelationshipsDropsConfiguredMultiSideRelationships(t *testing.T) {
	t.Parallel()

	got := prepareTablePackageRelationships(Relationships{
		"child": {{
			Name: "child_parent_through_bridge",
			Sides: []orm.RelSide{
				{From: "child", To: "bridge", Modify: "from"},
				{From: "bridge", To: "parent", Modify: "to"},
			},
		}},
	})
	if len(got["child"]) != 0 {
		t.Fatalf("expected configured multi-side relationship to be dropped, got %#v", got["child"])
	}
}

func TestPrepareTablePackageRelationshipsDropsReverseUniqueFK(t *testing.T) {
	t.Parallel()

	got := prepareTablePackageRelationships(Relationships{
		"parent": {{
			Name:  "child_parent_unique_fk",
			Sides: []orm.RelSide{{From: "parent", To: "child", Modify: "to", ToUnique: true}},
		}},
	})
	if len(got["parent"]) != 0 {
		t.Fatalf("expected reverse unique-FK relationship to be dropped, got %#v", got["parent"])
	}
}

func TestBreakRelationshipCyclesKeepsLexicallyFirstSource(t *testing.T) {
	t.Parallel()

	entityToProgram := orm.Relationship{
		Name:  "entity_loyalty_program_fk",
		Sides: []orm.RelSide{{From: "entity", To: "loyalty_program", ToUnique: true}},
	}
	programToEntity := orm.Relationship{
		Name:  "loyalty_program_entity_fk",
		Sides: []orm.RelSide{{From: "loyalty_program", To: "entity", ToUnique: true}},
	}

	got := breakRelationshipCycles(Relationships{
		"entity":          {entityToProgram},
		"loyalty_program": {programToEntity},
	})

	if len(got["entity"]) != 1 {
		t.Fatalf("expected lexically first entity edge to remain, got %#v", got["entity"])
	}
	if len(got["loyalty_program"]) != 0 {
		t.Fatalf("expected cycle-closing loyalty_program edge to be dropped, got %#v", got["loyalty_program"])
	}
}

func TestBreakRelationshipCyclesSortsByTableNameBeforeSchema(t *testing.T) {
	t.Parallel()

	got := breakRelationshipCycles(Relationships{
		"zeta.alpha": {{
			Name:  "alpha_beta_fk",
			Sides: []orm.RelSide{{From: "zeta.alpha", To: "aardvark.beta", ToUnique: true}},
		}},
		"aardvark.beta": {{
			Name:  "beta_alpha_fk",
			Sides: []orm.RelSide{{From: "aardvark.beta", To: "zeta.alpha", ToUnique: true}},
		}},
	})

	if len(got["zeta.alpha"]) != 1 || len(got["aardvark.beta"]) != 0 {
		t.Fatalf("expected alpha source edge to win regardless of schema, got %#v", got)
	}
}

func TestBreakRelationshipCyclesKeepsAcyclicEdges(t *testing.T) {
	t.Parallel()

	got := breakRelationshipCycles(Relationships{
		"entity": {{Name: "entity_cohort_fk", Sides: []orm.RelSide{{From: "entity", To: "cohort", ToUnique: true}}}},
		"store":  {{Name: "store_entity_fk", Sides: []orm.RelSide{{From: "store", To: "entity", ToUnique: true}}}},
	})

	if len(got["entity"]) != 1 || len(got["store"]) != 1 {
		t.Fatalf("expected all acyclic relationships to remain, got %#v", got)
	}
}
