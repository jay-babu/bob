package next

import (
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Dimension retains PostgreSQL lower bounds instead of silently assuming one.
type Dimension struct{ Length, LowerBound int }

// Array retains element nullability and dimensions. SQL NULL is represented by
// sql.Null[Array[T]], separately from an empty array or an array containing NULL.
type Array[T any] struct {
	Elements   []sql.Null[T]
	Dimensions []Dimension
}

func (a *Array[T]) Scan(src any) error {
	var text string
	switch v := src.(type) {
	case string:
		text = v
	case []byte:
		text = string(v)
	default:
		return fmt.Errorf("cannot scan %T into Array", src)
	}
	p := arrayParser{input: text}
	var explicit []Dimension
	for p.peek() == '[' {
		p.pos++
		lo, err := p.integer()
		if err != nil {
			return err
		}
		if !p.consume(':') {
			return fmt.Errorf("invalid array lower bound")
		}
		hi, err := p.integer()
		if err != nil || !p.consume(']') {
			return fmt.Errorf("invalid array upper bound")
		}
		if hi < lo || int64(hi)-int64(lo)+1 > int64(^uint(0)>>1) {
			return fmt.Errorf("invalid array dimension")
		}
		explicit = append(explicit, Dimension{hi - lo + 1, lo})
		if len(explicit) > 6 {
			return fmt.Errorf("too many array dimensions")
		}
	}
	if len(explicit) > 0 && !p.consume('=') {
		return fmt.Errorf("missing array dimension separator")
	}
	shape, tokens, err := p.group(0)
	if err != nil {
		return err
	}
	if p.pos != len(text) {
		return fmt.Errorf("trailing array input")
	}
	dims := make([]Dimension, len(shape))
	for i, length := range shape {
		if length == 0 && len(shape) > 1 {
			return fmt.Errorf("empty nested array dimension")
		}
		dims[i] = Dimension{length, 1}
	}
	if len(shape) == 1 && shape[0] == 0 {
		dims = nil
	}
	if len(explicit) > 0 {
		if len(explicit) != len(dims) {
			return fmt.Errorf("array dimension mismatch")
		}
		for i, d := range explicit {
			if d.Length != dims[i].Length {
				return fmt.Errorf("array shape mismatch")
			}
		}
		dims = explicit
	}
	elements := make([]sql.Null[T], len(tokens))
	for i, token := range tokens {
		if !token.null {
			if err := scanScalar(&elements[i].V, token.text); err != nil {
				return fmt.Errorf("array element %d: %w", i, err)
			}
			elements[i].Valid = true
		}
	}
	*a = Array[T]{elements, dims}
	return nil
}

func (a Array[T]) Value() (driver.Value, error) {
	dims := append([]Dimension(nil), a.Dimensions...)
	if len(dims) == 0 {
		if len(a.Elements) == 0 {
			return "{}", nil
		}
		dims = []Dimension{{len(a.Elements), 1}}
	}
	if len(dims) > 6 {
		return nil, fmt.Errorf("too many array dimensions")
	}
	count := 1
	prefix := false
	for _, d := range dims {
		if d.Length <= 0 || d.Length > int(^uint(0)>>1)/count || d.Length > 2147483647 || int64(d.LowerBound) < -2147483648 || int64(d.LowerBound) > 2147483647 || int64(d.LowerBound)+int64(d.Length)-1 > 2147483647 {
			return nil, fmt.Errorf("invalid array dimensions")
		}
		count *= d.Length
		prefix = prefix || d.LowerBound != 1
	}
	if count != len(a.Elements) {
		return nil, fmt.Errorf("array element count does not match dimensions")
	}
	var b strings.Builder
	if prefix {
		for _, d := range dims {
			fmt.Fprintf(&b, "[%d:%d]", d.LowerBound, d.LowerBound+d.Length-1)
		}
		b.WriteByte('=')
	}
	index := 0
	var group func(int) error
	group = func(depth int) error {
		b.WriteByte('{')
		for i := 0; i < dims[depth].Length; i++ {
			if i > 0 {
				b.WriteByte(',')
			}
			if depth+1 < len(dims) {
				if err := group(depth + 1); err != nil {
					return err
				}
				continue
			}
			e := a.Elements[index]
			index++
			if !e.Valid {
				b.WriteString("NULL")
				continue
			}
			text, err := arrayScalar(e.V)
			if err != nil {
				return err
			}
			b.WriteByte('"')
			for j := 0; j < len(text); j++ {
				c := text[j]
				if c == '"' || c == '\\' {
					b.WriteByte('\\')
				}
				b.WriteByte(c)
			}
			b.WriteByte('"')
		}
		b.WriteByte('}')
		return nil
	}
	if err := group(0); err != nil {
		return nil, err
	}
	return b.String(), nil
}

func arrayScalar[T any](value T) (string, error) {
	if raw, ok := any(value).(json.RawMessage); ok {
		if !json.Valid(raw) {
			return "", fmt.Errorf("invalid JSON array element")
		}
		return string(raw), nil
	}
	v, err := driver.DefaultParameterConverter.ConvertValue(value)
	if err != nil {
		return "", err
	}
	switch v := v.(type) {
	case string:
		return v, nil
	case []byte:
		return `\x` + hex.EncodeToString(v), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64), nil
	case bool:
		return strconv.FormatBool(v), nil
	case time.Time:
		return v.Format("2006-01-02 15:04:05.999999999Z07:00"), nil
	}
	return "", fmt.Errorf("unsupported or NULL array element %T", v)
}

type arrayToken struct {
	text string
	null bool
}
type arrayParser struct {
	input string
	pos   int
}

func (p *arrayParser) peek() byte {
	if p.pos >= len(p.input) {
		return 0
	}
	return p.input[p.pos]
}

func (p *arrayParser) consume(c byte) bool {
	if p.peek() != c {
		return false
	}
	p.pos++
	return true
}

func (p *arrayParser) integer() (int, error) {
	start := p.pos
	if p.peek() == '-' {
		p.pos++
	}
	for p.peek() >= '0' && p.peek() <= '9' {
		p.pos++
	}
	value, err := strconv.ParseInt(p.input[start:p.pos], 10, 32)
	return int(value), err
}

func (p *arrayParser) group(depth int) ([]int, []arrayToken, error) {
	if depth >= 6 || !p.consume('{') {
		return nil, nil, fmt.Errorf("invalid array group")
	}
	if p.consume('}') {
		return []int{0}, nil, nil
	}
	nested := p.peek() == '{'
	var shape []int
	var tokens []arrayToken
	count := 0
	for {
		if nested {
			child, values, err := p.group(depth + 1)
			if err != nil {
				return nil, nil, err
			}
			if count > 0 && !slices.Equal(child, shape) {
				return nil, nil, fmt.Errorf("ragged array")
			}
			shape = child
			tokens = append(tokens, values...)
		} else {
			token, err := p.token()
			if err != nil {
				return nil, nil, err
			}
			tokens = append(tokens, token)
		}
		count++
		if p.consume('}') {
			break
		}
		if !p.consume(',') {
			return nil, nil, fmt.Errorf("invalid array separator")
		}
		if (p.peek() == '{') != nested {
			return nil, nil, fmt.Errorf("mixed array dimensions")
		}
	}
	return append([]int{count}, shape...), tokens, nil
}

func (p *arrayParser) token() (arrayToken, error) {
	quoted := p.consume('"')
	escaped := false
	var b strings.Builder
	first, last := -1, 0
	for {
		c := p.peek()
		if c == 0 {
			return arrayToken{}, fmt.Errorf("unterminated array element")
		}
		if quoted && c == '"' {
			p.pos++
			return arrayToken{text: b.String()}, nil
		}
		if !quoted && (c == ',' || c == '}') {
			if first < 0 {
				return arrayToken{}, fmt.Errorf("empty unquoted array element")
			}
			text := b.String()[first:last]
			return arrayToken{text, !escaped && strings.EqualFold(text, "NULL")}, nil
		}
		if !quoted && (c == '{' || c == '"') {
			return arrayToken{}, fmt.Errorf("invalid unquoted array element")
		}
		p.pos++
		wasEscaped := false
		if c == '\\' {
			escaped = true
			wasEscaped = true
			c = p.peek()
			if c == 0 {
				return arrayToken{}, fmt.Errorf("unterminated array escape")
			}
			p.pos++
		}
		b.WriteByte(c)
		if !strings.ContainsRune(" \t\r\n\f\v", rune(c)) || wasEscaped {
			if first < 0 {
				first = b.Len() - 1
			}
			last = b.Len()
		}
	}
}
