package gen

import (
	"bytes"
	"io/fs"
	"strings"
	"testing"
	"text/template"

	"github.com/Masterminds/sprig/v3"
	"github.com/stephenafamo/bob/gen/drivers"
)

type testImporter map[string]struct{}

func (i testImporter) Import(pkgs ...string) string {
	for _, pkg := range pkgs {
		i[pkg] = struct{}{}
	}
	return ""
}

func (i testImporter) ImportList(pkgs []string) string {
	for _, pkg := range pkgs {
		i[pkg] = struct{}{}
	}
	return ""
}

func (i testImporter) ToList() []string {
	out := make([]string, 0, len(i))
	for pkg := range i {
		out = append(out, pkg)
	}
	return out
}

func Test_enumValToIdentifier(t *testing.T) {
	tests := []struct {
		val      string
		expected string
	}{
		{"in_progress", "InProgress"},
		{"in-progress", "InProgress"},
		{"in progress", "InProgress"},
		{"IN_PROGRESS", "InProgress"},
		{"in___-__progress", "InProgress"},
		{" in progress ", "InProgress"},
		// This is OK, because enum values are prefixed with the type name, e.g. TaskStatus1InProgress
		{"1-in-progress", "1InProgress"},
		{"start < end", "StartU3CEnd"},
	}
	for _, tt := range tests {
		t.Run(tt.val, func(t *testing.T) {
			if actual := enumValToIdentifier(tt.val); actual != tt.expected {
				t.Errorf("enumValToIdentifier(%q) = %q; want %q", tt.val, actual, tt.expected)
			}
		})
	}
}

func Test_enumValToScreamingSnakeCase(t *testing.T) {
	tests := []struct {
		val      string
		expected string
	}{
		{"in_progress", "IN_PROGRESS"},
		{"in-progress", "IN_PROGRESS"},
		{"in progress", "IN_PROGRESS"},
		{"IN_PROGRESS", "IN_PROGRESS"},
		{"in___-__progress", "IN______PROGRESS"},
		{" in progress ", "_IN_PROGRESS_"},
		{"1-in-progress", "1_IN_PROGRESS"},
		{"start < end", "START_U3c_END"},
	}
	for _, tt := range tests {
		t.Run(tt.val, func(t *testing.T) {
			if actual := enumValToScreamingSnakeCase(tt.val); actual != tt.expected {
				t.Errorf("enumValToScreamingSnakeCase(%q) = %q; want %q", tt.val, actual, tt.expected)
			}
		})
	}
}

func TestRelationshipMutationMethodsTemplateCanBeDisabled(t *testing.T) {
	content, err := fs.ReadFile(templates, "templates/models/table/011_rel_ops.go.tpl")
	if err != nil {
		t.Fatal(err)
	}

	tpl, err := template.New("rel_ops").
		Funcs(sprig.GenericFuncMap()).
		Funcs(templateFunctions).
		Parse(string(content))
	if err != nil {
		t.Fatal(err)
	}

	data := TemplateData[any, any, any]{
		Importer: testImporter{},
		Table: drivers.Table[any, any]{
			Constraints: drivers.Constraints[any]{
				Primary: &drivers.Constraint[any]{Columns: []string{"id"}},
			},
		},
		NoRelationshipMutationMethods: true,
	}

	var out bytes.Buffer
	if err := tpl.Execute(&out, data); err != nil {
		t.Fatal(err)
	}

	if got := strings.TrimSpace(out.String()); got != "" {
		t.Fatalf("expected relationship mutation methods template to be empty, got:\n%s", got)
	}
}

func TestSliceMutationMethodsTemplateCanBeDisabled(t *testing.T) {
	content, err := fs.ReadFile(templates, "templates/models/table/007_slice_methods.go.tpl")
	if err != nil {
		t.Fatal(err)
	}

	tpl, err := template.New("slice_methods").
		Funcs(sprig.GenericFuncMap()).
		Funcs(templateFunctions).
		Parse(string(content))
	if err != nil {
		t.Fatal(err)
	}

	data := TemplateData[any, any, any]{
		Dialect:  "psql",
		Importer: testImporter{},
		Table: drivers.Table[any, any]{
			Key: "widget",
			Constraints: drivers.Constraints[any]{
				Primary: &drivers.Constraint[any]{Columns: []string{"id"}},
			},
		},
		Aliases: drivers.Aliases{
			"widget": {
				UpPlural:   "Widgets",
				UpSingular: "Widget",
			},
		},
		NoSliceMutationMethods: true,
	}

	var out bytes.Buffer
	if err := tpl.Execute(&out, data); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	if !strings.Contains(got, "func (o WidgetSlice) AfterQueryHook(") {
		t.Fatalf("expected slice AfterQueryHook to remain, got:\n%s", got)
	}

	for _, removed := range []string{"UpdateAll", "DeleteAll", "ReloadAll", "UpdateMod", "DeleteMod", "pkIN", "copyMatchingRows"} {
		if strings.Contains(got, removed) {
			t.Fatalf("expected %s to be omitted, got:\n%s", removed, got)
		}
	}
}
