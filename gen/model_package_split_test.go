package gen

import (
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stephenafamo/bob/gen/drivers"
	"github.com/stephenafamo/bob/orm"
)

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
	facadesPackage := "example.com/internal/factorymodels"
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
	wantPackage := "example.com/internal/factorymodels/public/entity"
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
