package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// setValue is one parsed --set key=value pair.
type setValue struct {
	path  []string
	value interface{}
}

// parseSetFlags parses --set key=value flags. The value is parsed with
// parseScalarValue and the key with splitKeyPath.
func parseSetFlags(flags []string) ([]setValue, error) {
	out := make([]setValue, 0, len(flags))
	for _, kv := range flags {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
			return nil, fmt.Errorf("invalid --set format %q: expected key=value", kv)
		}
		path, err := splitKeyPath(parts[0])
		if err != nil {
			return nil, err
		}
		out = append(out, setValue{path: path, value: parseScalarValue(parts[1])})
	}
	return out, nil
}

// parseScalarValue parses a --set value like helm's strvals typedVal:
// "true", "false" and "null" (any case) become true, false and nil; "0"
// becomes 0; a value that does not start with "0" and parses as a base-10
// int64 becomes that integer ("+1" and "-01" too, as in helm). Every other
// value stays a string ("1.10", "0123", "1e3", "").
func parseScalarValue(s string) interface{} {
	switch {
	case strings.EqualFold(s, "true"):
		return true
	case strings.EqualFold(s, "false"):
		return false
	case strings.EqualFold(s, "null"):
		return nil
	case s == "0":
		return int64(0)
	}
	if s != "" && s[0] != '0' {
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return i
		}
	}
	return s
}

// splitKeyPath splits a dot-separated key into its parts. A backslash
// escapes a dot ("a\.b" is the single key "a.b") or a backslash. List
// indexes ("a[0]") are refused.
func splitKeyPath(key string) ([]string, error) {
	if strings.ContainsAny(key, "[]") {
		return nil, fmt.Errorf("invalid key %q: list indexes are not supported; use --file", key)
	}
	var parts []string
	var cur strings.Builder
	for i := 0; i < len(key); i++ {
		ch := key[i]
		switch {
		case ch == '\\' && i+1 < len(key) && (key[i+1] == '.' || key[i+1] == '\\'):
			cur.WriteByte(key[i+1])
			i++
		case ch == '.':
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(ch)
		}
	}
	parts = append(parts, cur.String())
	for _, p := range parts {
		if p == "" {
			return nil, fmt.Errorf("invalid key %q: empty key part", key)
		}
	}
	return parts, nil
}

// setNestedValue sets a value in a nested map using a dot-separated key
// path (see splitKeyPath).
func setNestedValue(m map[string]interface{}, key string, value interface{}) error {
	path, err := splitKeyPath(key)
	if err != nil {
		return err
	}
	setNestedPath(m, path, value)
	return nil
}

// setNestedPath sets a value in a nested map. A non-map value on the path
// is replaced by a map.
func setNestedPath(m map[string]interface{}, path []string, value interface{}) {
	for i, part := range path {
		if i == len(path)-1 {
			m[part] = value
			return
		}
		next, ok := m[part].(map[string]interface{})
		if !ok {
			next = map[string]interface{}{}
			m[part] = next
		}
		m = next
	}
}

// unsetNestedValue removes a dot-path key (see splitKeyPath) from a nested
// map and removes parent maps that become empty. It returns false when the
// key is not set or invalid.
func unsetNestedValue(m map[string]interface{}, key string) bool {
	path, err := splitKeyPath(key)
	if err != nil {
		return false
	}
	return unsetNestedPath(m, path)
}

// unsetNestedPath removes a key path from a nested map and removes parent
// maps that become empty. It returns false when the key is not set.
func unsetNestedPath(m map[string]interface{}, path []string) bool {
	if len(path) == 1 {
		if _, ok := m[path[0]]; !ok {
			return false
		}
		delete(m, path[0])
		return true
	}
	sub, ok := m[path[0]].(map[string]interface{})
	if !ok || !unsetNestedPath(sub, path[1:]) {
		return false
	}
	if len(sub) == 0 {
		delete(m, path[0])
	}
	return true
}

// normalizeYAML converts decoded YAML or JSON so it can be encoded as JSON
// and merged: every map becomes map[string]interface{} (non-string keys are
// formatted with fmt), recursively, also inside lists, and a json.Number
// becomes a Go number (see jsonNumberValue).
func normalizeYAML(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(t))
		for k, val := range t {
			out[k] = normalizeYAML(val)
		}
		return out
	case map[interface{}]interface{}:
		out := make(map[string]interface{}, len(t))
		for k, val := range t {
			out[fmt.Sprint(k)] = normalizeYAML(val)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, val := range t {
			out[i] = normalizeYAML(val)
		}
		return out
	case json.Number:
		return jsonNumberValue(t)
	default:
		return v
	}
}

// parseValuesDocument parses a JSON or YAML document of Helm values into a
// normalized map. An empty document is an empty map; a document that is
// not a mapping is an error.
func parseValuesDocument(data []byte) (map[string]interface{}, error) {
	var doc interface{}
	if err := decodeJSONNumbers(data, &doc); err != nil {
		doc = nil
		if yamlErr := yaml.Unmarshal(data, &doc); yamlErr != nil {
			return nil, fmt.Errorf("json: %v; yaml: %w", err, yamlErr)
		}
	}
	switch m := normalizeYAML(doc).(type) {
	case nil:
		return map[string]interface{}{}, nil
	case map[string]interface{}:
		return m, nil
	default:
		return nil, fmt.Errorf("the document is not a mapping of keys to values")
	}
}

// decodeJSONNumbers decodes one JSON document with UseNumber, so integers
// above 2^53 keep their exact value. Trailing data is an error.
func decodeJSONNumbers(data []byte, v *interface{}) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return fmt.Errorf("unexpected data after the JSON document")
	}
	return nil
}

// jsonNumberValue converts a json.Number to an int64 when it is an integer
// in range, else to a float64, else (out of float range) keeps the text.
func jsonNumberValue(n json.Number) interface{} {
	if i, err := n.Int64(); err == nil {
		return i
	}
	if f, err := n.Float64(); err == nil {
		return f
	}
	return n.String()
}
