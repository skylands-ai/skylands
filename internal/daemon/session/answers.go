package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"

	v1 "github.com/skylands-ai/skylands/api/v1"
	"github.com/skylands-ai/skylands/internal/canonical"
)

// maxSafeInteger is the largest integer a JSON number carries exactly in
// a browser, 2^53.
const maxSafeInteger = 1 << 53

// CheckAnswers checks answers to a question against its form, and returns
// them as canonical JSON (RFC 8785), which is what is signed, recorded,
// and given to the agent. The answers are an object of values by field
// key: only the form's fields; one of a choice's options, or for a
// multiple choice an array of them; a number for a number, a whole one
// within ±2^53 for an integer, and true or false for a boolean. A field
// left unanswered is left out, so an empty string, an empty array, or
// null is refused, and a required field must be answered.
func CheckAnswers(fields []v1.QuestionField, answers json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(answers)) == 0 {
		return nil, errors.New("answers must be a JSON object of values by field")
	}
	// Canonical form refuses what isn't I-JSON, such as a key given twice.
	out, err := canonical.Transform(answers)
	if err != nil {
		return nil, fmt.Errorf("answers must be a JSON object of values by field: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	dec.UseNumber()
	var values map[string]any
	if err := dec.Decode(&values); err != nil || values == nil {
		return nil, errors.New("answers must be a JSON object of values by field")
	}
	byKey := make(map[string]v1.QuestionField, len(fields))
	for _, f := range fields {
		byKey[f.Key] = f
	}
	for k, v := range values {
		f, ok := byKey[k]
		if !ok {
			return nil, fmt.Errorf("the question has no field %q", k)
		}
		if err := checkValue(f, v); err != nil {
			return nil, fmt.Errorf("%s: %w", fieldName(f), err)
		}
	}
	for _, f := range fields {
		if _, ok := values[f.Key]; f.Required && !ok {
			return nil, fmt.Errorf("%s needs an answer", fieldName(f))
		}
	}
	return out, nil
}

func fieldName(f v1.QuestionField) string {
	if f.Title != "" {
		return f.Title
	}
	return f.Key
}

// checkValue checks one answer against its field.
func checkValue(f v1.QuestionField, v any) error {
	if v == nil {
		return errors.New("null isn't an answer; leave out a field you don't answer")
	}
	if len(f.Options) > 0 {
		if f.Type == "array" {
			list, ok := v.([]any)
			if !ok {
				return errors.New("must be a list of the options chosen")
			}
			if len(list) == 0 {
				return errors.New("an empty list isn't an answer; leave out a field you don't answer")
			}
			seen := map[string]bool{}
			for _, it := range list {
				s, ok := it.(string)
				if !ok || !isOption(f, s) {
					return fmt.Errorf("%v isn't one of the options", it)
				}
				if seen[s] {
					return fmt.Errorf("%q is chosen twice", s)
				}
				seen[s] = true
			}
			return nil
		}
		s, ok := v.(string)
		if !ok || !isOption(f, s) {
			return fmt.Errorf("%v isn't one of the options", v)
		}
		return nil
	}
	switch f.Type {
	case "number", "integer":
		n, ok := v.(json.Number)
		if !ok {
			return fmt.Errorf("must be a number, not %v", v)
		}
		x, err := strconv.ParseFloat(string(n), 64)
		if err != nil || math.IsInf(x, 0) || math.IsNaN(x) {
			return fmt.Errorf("%s isn't a number", n)
		}
		if f.Type == "integer" && (x != math.Trunc(x) || math.Abs(x) > maxSafeInteger) {
			return fmt.Errorf("%s isn't a whole number within ±2^53", n)
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("must be true or false, not %v", v)
		}
	case "array":
		list, ok := v.([]any)
		if !ok || len(list) == 0 {
			return errors.New("must be a list with something in it; leave out a field you don't answer")
		}
		for _, it := range list {
			if s, ok := it.(string); !ok || s == "" {
				return fmt.Errorf("%v isn't text", it)
			}
		}
	default:
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("must be text, not %v", v)
		}
		if s == "" {
			return errors.New("empty text isn't an answer; leave out a field you don't answer")
		}
	}
	return nil
}

func isOption(f v1.QuestionField, s string) bool {
	for _, o := range f.Options {
		if o.Value == s {
			return true
		}
	}
	return false
}
