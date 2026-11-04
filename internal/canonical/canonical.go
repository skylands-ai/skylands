// Package canonical writes JSON in the JSON Canonicalization Scheme of RFC
// 8785, JCS: no whitespace, object properties sorted by their UTF-16 code
// units, and strings and numbers written as ECMAScript's JSON.stringify
// writes them. The same data gives the same bytes in Go and in a browser,
// so a signature over them verifies in both.
package canonical

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Marshal is v as encoding/json encodes it, in canonical form.
func Marshal(v any) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return Transform(data)
}

// Transform rewrites the JSON in data in canonical form. It refuses JSON
// that isn't I-JSON, as JCS requires: invalid Unicode, lone surrogates,
// duplicate property names, and numbers that aren't IEEE 754 doubles.
func Transform(data []byte) ([]byte, error) {
	if !utf8.Valid(data) {
		return nil, errors.New("canonical: the JSON is not valid UTF-8")
	}
	if err := checkSurrogates(data); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := parse(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err == nil {
		return nil, errors.New("canonical: more than one JSON value")
	}
	var b bytes.Buffer
	if err := write(&b, v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

type member struct {
	key string
	val any
}

// object keeps a JSON object's members, so duplicates can be refused and
// order decided when written.
type object []member

func parse(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("canonical: %w", err)
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			var obj object
			seen := map[string]bool{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, fmt.Errorf("canonical: %w", err)
				}
				key := kt.(string)
				if seen[key] {
					return nil, fmt.Errorf("canonical: duplicate property %q", key)
				}
				seen[key] = true
				val, err := parse(dec)
				if err != nil {
					return nil, err
				}
				obj = append(obj, member{key, val})
			}
			_, err := dec.Token()
			return obj, err
		case '[':
			arr := []any{}
			for dec.More() {
				val, err := parse(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, val)
			}
			_, err := dec.Token()
			return arr, err
		}
		return nil, fmt.Errorf("canonical: unexpected %v", t)
	default:
		return t, nil
	}
}

func write(b *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(t))
	case string:
		writeString(b, t)
	case json.Number:
		f, err := strconv.ParseFloat(string(t), 64)
		if err != nil {
			return fmt.Errorf("canonical: %s is not an IEEE 754 double", t)
		}
		s, err := Number(f)
		if err != nil {
			return err
		}
		b.WriteString(s)
	case []any:
		b.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := write(b, e); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case object:
		sorted := append(object(nil), t...)
		sort.Slice(sorted, func(i, j int) bool { return lessUTF16(sorted[i].key, sorted[j].key) })
		b.WriteByte('{')
		for i, m := range sorted {
			if i > 0 {
				b.WriteByte(',')
			}
			writeString(b, m.key)
			b.WriteByte(':')
			if err := write(b, m.val); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	default:
		return fmt.Errorf("canonical: unexpected %T", v)
	}
	return nil
}

// lessUTF16 orders strings by their UTF-16 code units, as JCS sorts
// property names.
func lessUTF16(a, b string) bool {
	ua, ub := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			return ua[i] < ub[i]
		}
	}
	return len(ua) < len(ub)
}

// writeString writes s as JSON.stringify does: quotes, backslashes, and
// control characters escaped, everything else as it is.
func writeString(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

// Number writes f as ECMAScript's Number.prototype.toString does: the
// shortest digits that read back as f, in plain notation for exponents
// from -7 to 21 and in exponent notation otherwise.
func Number(f float64) (string, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "", errors.New("canonical: NaN and Infinity are not JSON numbers")
	}
	if f == 0 {
		return "0", nil // and minus zero too
	}
	sign := ""
	if f < 0 {
		sign, f = "-", -f
	}
	// The shortest digits, with the exponent of the first one.
	e := strconv.FormatFloat(f, 'e', -1, 64)
	mant, expText, _ := strings.Cut(e, "e")
	digits := strings.Replace(mant, ".", "", 1)
	exp, err := strconv.Atoi(expText)
	if err != nil {
		return "", err
	}
	k, n := len(digits), exp+1 // n is where the decimal point falls
	switch {
	case k <= n && n <= 21:
		return sign + digits + strings.Repeat("0", n-k), nil
	case 0 < n && n <= 21:
		return sign + digits[:n] + "." + digits[n:], nil
	case -6 < n && n <= 0:
		return sign + "0." + strings.Repeat("0", -n) + digits, nil
	}
	es := "+"
	if n-1 < 0 {
		es = "-"
	}
	abs := strconv.Itoa(int(math.Abs(float64(n - 1))))
	if k == 1 {
		return sign + digits + "e" + es + abs, nil
	}
	return sign + digits[:1] + "." + digits[1:] + "e" + es + abs, nil
}

// checkSurrogates refuses \u escapes in strings that aren't a high
// surrogate followed by a low one, which a decoder would otherwise quietly
// replace.
func checkSurrogates(data []byte) error {
	inString := false
	hex4 := func(i int) (uint16, bool) {
		if i+4 > len(data) {
			return 0, false
		}
		v, err := strconv.ParseUint(string(data[i:i+4]), 16, 16)
		return uint16(v), err == nil
	}
	for i := 0; i < len(data); i++ {
		c := data[i]
		if !inString {
			if c == '"' {
				inString = true
			}
			continue
		}
		switch c {
		case '"':
			inString = false
		case '\\':
			if i+1 >= len(data) {
				return errors.New("canonical: the JSON ends inside a string")
			}
			if data[i+1] != 'u' {
				i++
				continue
			}
			u, ok := hex4(i + 2)
			if !ok {
				return errors.New("canonical: a bad \\u escape")
			}
			i += 5
			switch {
			case u >= 0xDC00 && u <= 0xDFFF:
				return errors.New("canonical: a lone low surrogate")
			case u >= 0xD800 && u <= 0xDBFF:
				if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
					return errors.New("canonical: a lone high surrogate")
				}
				low, ok := hex4(i + 3)
				if !ok || low < 0xDC00 || low > 0xDFFF {
					return errors.New("canonical: a lone high surrogate")
				}
				i += 6
			}
		}
	}
	return nil
}
