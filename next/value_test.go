package next_test

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	n "github.com/stephenafamo/bob/next"
)

func TestDecimalAndStructuredJSON(t *testing.T) {
	for _, text := range []string{"0", "-0.000", "123456789012345678901234567890.1234567890", "1e1000", ".5", "NaN", "Infinity"} {
		value, err := n.ParseDecimal(text)
		if err != nil {
			t.Fatal(err)
		}
		require(t, value.String(), text)
		encoded, err := value.Value()
		if err != nil {
			t.Fatal(err)
		}
		var scanned n.Decimal
		if err = scanned.Scan(encoded); err != nil {
			t.Fatal(err)
		}
		require(t, scanned, value)
		encodedJSON, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var decodedJSON n.Decimal
		if err = json.Unmarshal(encodedJSON, &decodedJSON); err != nil {
			t.Fatal(err)
		}
		require(t, decodedJSON, value)
	}
	for _, text := range []string{"", ".", "1e", "1 2", "--2", "1;SELECT 2"} {
		if _, err := n.ParseDecimal(text); err == nil {
			t.Fatal("invalid decimal accepted")
		}
	}
	var value n.Decimal
	if err := value.Scan(nil); err == nil {
		t.Fatal("NULL decimal accepted without nullable wrapper")
	}
	var data n.JSON[struct{ Name string }]
	if err := data.Scan(`{"Name":"example"}`); err != nil {
		t.Fatal(err)
	}
	require(t, data.V.Name, "example")
	if err := data.Scan(`invalid`); err == nil {
		t.Fatal("invalid JSON accepted")
	}
}

func TestJSONV2DefaultsAndIndependentScans(t *testing.T) {
	for _, value := range []struct {
		encoded interface{ Value() (driver.Value, error) }
		want    string
	}{
		{n.JSON[[]string]{}, "[]"}, {n.JSON[map[string]string]{}, "{}"},
	} {
		got, err := value.encoded.Value()
		if err != nil {
			t.Fatal(err)
		}
		require(t, got, driver.Value(value.want))
	}
	type payload struct {
		Label  string
		Fields map[string]string
		Items  []int
	}
	var value n.JSON[payload]
	if err := value.Scan(`{"Label":"old","Fields":{"old":"kept"},"Items":[1,2]}`); err != nil {
		t.Fatal(err)
	}
	previous := value.V
	if err := value.Scan(`{"Fields":{"new":"fresh"}}`); err != nil {
		t.Fatal(err)
	}
	require(t, value.V.Label, "")
	require(t, value.V.Items, []int(nil))
	require(t, value.V.Fields, map[string]string{"new": "fresh"})
	require(t, previous.Fields, map[string]string{"old": "kept"})
	require(t, previous.Items, []int{1, 2})
	for _, text := range []string{`{"Fields":{"a":"one","a":"two"}}`, `{"Label":"one"} {"Label":"two"}`} {
		if err := value.Scan(text); err == nil {
			t.Fatal("invalid JSON accepted")
		}
		require(t, value.V.Fields, map[string]string{"new": "fresh"})
	}
}

func TestArrayRoundTripShapeBoundsAndNulls(t *testing.T) {
	for _, text := range []string{`{}`, `{"a,b","quote\"slash\\",NULL,"NULL",""}`, `[0:1][-2:-1]={{"a","b"},{"c",NULL}}`, `{NULL,null,"null",N\ULL}`, `{\ leading\ , }`} {
		var value n.Array[string]
		if err := value.Scan(text); err != nil {
			t.Fatal(text, err)
		}
		encoded, err := value.Value()
		if err != nil {
			t.Fatal(err)
		}
		var again n.Array[string]
		if err = again.Scan(encoded); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(value, again) {
			t.Fatalf("%s: %#v != %#v", text, value, again)
		}
	}
	var array n.Array[string]
	if err := array.Scan(`{NULL,"NULL"}`); err != nil {
		t.Fatal(err)
	}
	require(t, array.Elements[0].Valid, false)
	require(t, array.Elements[1].Valid, true)
	for _, text := range []string{``, `{a`, `{"a}`, `{a,}`, `{,a}`, `{{a},{a,b}}`, `{{a},b}`, `{{},{}}`, `[0:3]={a,b}`, `[1:1]{a}`, `{a}extra`, `[2147483648:2147483648]={a}`} {
		if err := array.Scan(text); err == nil {
			t.Fatal("invalid array accepted:", text)
		}
	}
	if err := array.Scan(nil); err == nil {
		t.Fatal("NULL array accepted without nullable wrapper")
	}
	bad := n.Array[int64]{Elements: []sql.Null[int64]{{V: 1, Valid: true}}, Dimensions: []n.Dimension{{Length: 2, LowerBound: 1}}}
	if _, err := bad.Value(); err == nil {
		t.Fatal("invalid shape accepted")
	}
	var dates n.Array[time.Time]
	if err := dates.Scan(`{"2026-10-31","2026-10-31 12:34:56+00"}`); err != nil {
		t.Fatal(err)
	}
	var bytes n.Array[[]byte]
	if err := bytes.Scan(`{"\\x00ff",NULL}`); err != nil {
		t.Fatal(err)
	}
	require(t, bytes.Elements[0].V, []byte{0, 255})
	var raw n.Array[json.RawMessage]
	if err := raw.Scan(`{"{\"x\":1}",NULL}`); err != nil {
		t.Fatal(err)
	}
	require(t, string(raw.Elements[0].V), `{"x":1}`)
}

func FuzzArrayRoundTrip(f *testing.F) {
	for _, seed := range []string{`{}`, `{a,NULL,"NULL"}`, `[-1:0]={{a},{b}}`, `{"a,b","quote\"slash\\"}`, `{\ leading\ , }`, `{{},{}}`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		if len(text) > 4096 {
			return
		}
		var value n.Array[string]
		if value.Scan(text) != nil {
			return
		}
		encoded, err := value.Value()
		if err != nil {
			t.Fatal(err)
		}
		var again n.Array[string]
		if err = again.Scan(encoded); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(value, again) {
			t.Fatal("array changed across encoding")
		}
	})
}
