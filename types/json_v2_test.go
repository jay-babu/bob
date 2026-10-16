package types

import (
	"testing"
)

func TestJSONUsesV2Defaults(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{"nil slice", []string(nil), "[]"},
		{"nil map", map[string]string(nil), "{}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NewJSON(tc.value).MarshalJSON()
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("got %s; want %s", got, tc.want)
			}
		})
	}
	var value JSON[map[string]int]
	if err := value.Scan([]byte(`{"id":1,"id":2}`)); err == nil {
		t.Fatal("duplicate object names accepted")
	}
	if err := value.Scan([]byte(`{"id":1} {"id":2}`)); err == nil {
		t.Fatal("multiple JSON values accepted")
	}
}
