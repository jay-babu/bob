package next

import (
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Decimal preserves the database's exact decimal text, including scale. SQL
// arithmetic stays on the server; no binary floating-point conversion occurs.
type Decimal struct{ text string }

func ParseDecimal(text string) (Decimal, error) {
	if text == "NaN" || text == "Infinity" || text == "-Infinity" {
		return Decimal{text}, nil
	}
	i := 0
	if len(text) > 0 && (text[0] == '+' || text[0] == '-') {
		i++
	}
	digits := 0
	for i < len(text) && text[i] >= '0' && text[i] <= '9' {
		i++
		digits++
	}
	if i < len(text) && text[i] == '.' {
		i++
		for i < len(text) && text[i] >= '0' && text[i] <= '9' {
			i++
			digits++
		}
	}
	if digits == 0 {
		return Decimal{}, fmt.Errorf("invalid decimal")
	}
	if i < len(text) && (text[i] == 'e' || text[i] == 'E') {
		i++
		if i < len(text) && (text[i] == '+' || text[i] == '-') {
			i++
		}
		start := i
		for i < len(text) && text[i] >= '0' && text[i] <= '9' {
			i++
		}
		if i == start {
			return Decimal{}, fmt.Errorf("invalid decimal exponent")
		}
	}
	if i != len(text) {
		return Decimal{}, fmt.Errorf("invalid decimal")
	}
	return Decimal{text}, nil
}
func (d Decimal) String() string {
	if d.text == "" {
		return "0"
	}
	return d.text
}
func (d Decimal) Value() (driver.Value, error) { return d.String(), nil }
func (d *Decimal) Scan(src any) error {
	var text string
	switch v := src.(type) {
	case string:
		text = v
	case []byte:
		text = string(v)
	case int64:
		text = strconv.FormatInt(v, 10)
	default:
		return fmt.Errorf("cannot scan %T into Decimal", src)
	}
	value, err := ParseDecimal(text)
	if err != nil {
		return err
	}
	*d = value
	return nil
}
func (d Decimal) MarshalJSON() ([]byte, error) {
	return jsonv2.Marshal(d.String())
}
func (d *Decimal) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		var text string
		if err := jsonv2.Unmarshal(data, &text); err != nil {
			return err
		}
		return d.Scan(text)
	}
	return d.Scan(string(data))
}

// Domain is embedded by generated database-domain types. Each generated wrapper
// is a distinct Go type, while scanning and value conversion remain shared.
type Domain[T any] struct{ V T }

func (d *Domain[T]) Scan(src any) error { return scanScalar(&d.V, src) }
func (d Domain[T]) Value() (driver.Value, error) {
	return driver.DefaultParameterConverter.ConvertValue(d.V)
}
func (d Domain[T]) MarshalJSON() ([]byte, error) { return jsonv2.Marshal(d.V) }
func (d *Domain[T]) UnmarshalJSON(data []byte) error {
	var value T
	if err := jsonv2.Unmarshal(data, &value); err != nil {
		return err
	}
	d.V = value
	return nil
}

// JSON gives application-owned structured JSON the standard scanner/valuer
// contract. Use sql.Null[JSON[T]] for SQL NULL, independently of JSON null.
type JSON[T any] struct{ V T }

func (j *JSON[T]) Scan(src any) error {
	switch v := src.(type) {
	case string:
		return j.UnmarshalJSON([]byte(v))
	case []byte:
		return j.UnmarshalJSON(v)
	default:
		return fmt.Errorf("cannot scan %T into JSON", src)
	}
}
func (j JSON[T]) Value() (driver.Value, error) {
	data, err := jsonv2.Marshal(j.V)
	return string(data), err
}
func (j JSON[T]) MarshalJSON() ([]byte, error) { return jsonv2.Marshal(j.V) }
func (j *JSON[T]) UnmarshalJSON(data []byte) error {
	var value T
	if err := jsonv2.Unmarshal(data, &value); err != nil {
		return err
	}
	j.V = value
	return nil
}

func scanScalar[T any](dest *T, src any) error {
	if scanner, ok := any(dest).(sql.Scanner); ok {
		return scanner.Scan(src)
	}
	switch value := any(dest).(type) {
	case *time.Time:
		if v, ok := src.(time.Time); ok {
			*value = v
			return nil
		}
		text, ok := src.(string)
		if !ok {
			return fmt.Errorf("cannot scan %T into time", src)
		}
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999Z07:00:00", "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999Z07", "2006-01-02 15:04:05.999999999", "2006-01-02"} {
			if parsed, err := time.Parse(layout, text); err == nil {
				*value = parsed.UTC()
				return nil
			}
		}
		return fmt.Errorf("invalid PostgreSQL time value")
	case *[]byte:
		if text, ok := src.(string); ok && strings.HasPrefix(text, `\x`) {
			bytes, err := hex.DecodeString(text[2:])
			if err != nil {
				return err
			}
			*value = bytes
			return nil
		}
	case *json.RawMessage:
		var bytes []byte
		switch v := src.(type) {
		case string:
			bytes = []byte(v)
		case []byte:
			bytes = append([]byte(nil), v...)
		default:
			return fmt.Errorf("cannot scan %T into JSON text", src)
		}
		if !json.Valid(bytes) {
			return fmt.Errorf("invalid JSON text")
		}
		*value = bytes
		return nil
	}
	var value sql.Null[T]
	if err := value.Scan(src); err != nil {
		return err
	}
	if !value.Valid {
		return fmt.Errorf("SQL NULL requires a nullable destination")
	}
	*dest = value.V
	return nil
}
