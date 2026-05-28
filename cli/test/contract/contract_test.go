// Package contract validates that stackctl's pkg/types request structs
// match the backend's published OpenAPI (Swagger 2.0) schema.
//
// Why this exists: every other test layer in stackctl (unit, integration,
// e2e) stubs the backend with httptest and decodes request bodies into
// stackctl's OWN types — so a JSON-tag drift between stackctl and the
// backend decodes cleanly in tests but 400s the moment a real backend
// reads it. Four shipped wire-shape bugs (#95, #98, k8s-sm#264, the
// BulkOperationResult shape) all slipped through that blind spot.
//
// The schema is vendored at testdata/swagger.json; refresh it via the
// refresh-swagger.sh script when the backend ships a new field.
package contract

import (
	_ "embed"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/omattsson/stackctl/cli/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//go:embed testdata/swagger.json
var swaggerJSON []byte

// swaggerSchema is the minimum subset of OpenAPI 2.0 we need to walk
// definitions. Properties are decoded as a raw map so per-property type
// strings stay accessible without modelling every JSON Schema corner.
type swaggerSchema struct {
	Definitions map[string]swaggerDefinition `json:"definitions"`
}

type swaggerDefinition struct {
	Type       string                       `json:"type"`
	Required   []string                     `json:"required"`
	Properties map[string]swaggerPropertyV2 `json:"properties"`
}

type swaggerPropertyV2 struct {
	Type   string         `json:"type"`
	Format string         `json:"format,omitempty"`
	Items  *itemsRef      `json:"items,omitempty"`
	Ref    string         `json:"$ref,omitempty"`
	Extras map[string]any `json:"-"` // unused, kept for clarity
}

type itemsRef struct {
	Type string `json:"type"`
	Ref  string `json:"$ref,omitempty"`
}

// contractCase pairs a Go request struct with the swagger definition
// it must conform to. ExcludeGoFields is the escape hatch for the
// (rare) case where stackctl intentionally carries a field the backend
// doesn't validate — e.g. write-only credentials the server elides on
// read, or stackctl-side display aliases.
type contractCase struct {
	name           string
	goType         any
	swaggerDef     string
	excludeGoTags  []string // Go json tags to skip on the "Go ⊂ swagger" check
	excludeRequire []string // swagger required fields to skip on the "required ⊂ Go" check
}

// TestRequestSchemas_MatchBackend asserts that every stackctl request
// type in the table matches the backend's OpenAPI definition both ways:
//
//  1. Every json tag on the Go struct exists as a property in the
//     swagger schema. Catches stale or typoed Go-side fields.
//  2. Every "required" field in the swagger schema has a matching Go
//     json tag. Catches missing-required-field bugs.
//  3. Field type alignment — Go reflect.Kind maps to the swagger
//     property's `type` string.
//
// Failure messages name the drifting field on both sides so the fix
// (rename, add, or update the exclusion list) is one diff away.
func TestRequestSchemas_MatchBackend(t *testing.T) {
	t.Parallel()
	schema := loadSwagger(t)

	cases := []contractCase{
		{
			name:       "CreateClusterRequest",
			goType:     types.CreateClusterRequest{},
			swaggerDef: "handlers.CreateClusterRequest",
		},
		{
			name:       "UpdateClusterRequest",
			goType:     types.UpdateClusterRequest{},
			swaggerDef: "handlers.UpdateClusterRequest",
		},
		{
			name:       "BulkInstancesRequest",
			goType:     types.BulkInstancesRequest{},
			swaggerDef: "handlers.BulkOperationRequest",
		},
		{
			name:       "BulkTemplatesRequest",
			goType:     types.BulkTemplatesRequest{},
			swaggerDef: "handlers.BulkTemplateRequest",
		},
		{
			name:       "RegisterRequest",
			goType:     types.RegisterRequest{},
			swaggerDef: "handlers.RegisterRequest",
		},
		{
			name:       "LoginRequest",
			goType:     types.LoginRequest{},
			swaggerDef: "handlers.LoginRequest",
		},
		{
			name:       "CreateAPIKeyRequest",
			goType:     types.CreateAPIKeyRequest{},
			swaggerDef: "handlers.CreateAPIKeyRequest",
		},
		{
			name:       "ResetPasswordRequest",
			goType:     types.ResetPasswordRequest{},
			swaggerDef: "handlers.ResetPasswordRequest",
		},
		{
			name:       "CreateCleanupPolicyRequest",
			goType:     types.CreateCleanupPolicyRequest{},
			swaggerDef: "models.CleanupPolicy",
			// models.CleanupPolicy is the backend's read-side type with
			// timestamps + id; the write-side stackctl request
			// intentionally omits them. They're not "required" in
			// swagger either, so no required-side exclusion needed.
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			def, ok := schema.Definitions[tc.swaggerDef]
			require.Truef(t, ok, "swagger schema is missing definition %q (refresh swagger.json?)", tc.swaggerDef)
			assertFieldsMatch(t, tc.goType, def, tc.excludeGoTags, tc.excludeRequire)
		})
	}
}

func loadSwagger(t *testing.T) swaggerSchema {
	t.Helper()
	var s swaggerSchema
	require.NoError(t, json.Unmarshal(swaggerJSON, &s), "parse vendored swagger.json")
	require.NotEmptyf(t, s.Definitions, "swagger.json has no .definitions — refresh script broken?")
	return s
}

// assertFieldsMatch walks every exported field of goType, reads its
// `json:` tag, and asserts the field exists in def.Properties with a
// compatible type. The reverse direction is handled by the required-set
// check at the end.
func assertFieldsMatch(t *testing.T, goType any, def swaggerDefinition, excludeGoTags, excludeRequire []string) {
	t.Helper()

	excludeGo := make(map[string]struct{}, len(excludeGoTags))
	for _, n := range excludeGoTags {
		excludeGo[n] = struct{}{}
	}
	excludeReq := make(map[string]struct{}, len(excludeRequire))
	for _, n := range excludeRequire {
		excludeReq[n] = struct{}{}
	}

	tags := collectJSONTags(reflect.TypeOf(goType))

	// 1. Go ⊂ swagger.Properties — every Go tag must exist as a
	// property in the swagger definition (catches typos, stale fields).
	for name, gf := range tags {
		if _, skip := excludeGo[name]; skip {
			continue
		}
		prop, ok := def.Properties[name]
		if !assert.Truef(t, ok,
			"Go struct field %s (json:%q) has no matching property in swagger schema — typo, or backend doesn't accept this field",
			gf.GoName, name) {
			continue
		}
		// 3. Type alignment.
		assertTypeCompatible(t, name, gf, prop)
	}

	// 2. swagger.Required ⊂ Go — every "required" swagger field must
	// have a matching json tag in Go (catches missing required fields).
	for _, req := range def.Required {
		if _, skip := excludeReq[req]; skip {
			continue
		}
		_, ok := tags[req]
		assert.Truef(t, ok,
			"swagger schema requires field %q but the Go struct has no field with that json tag — request will fail validation",
			req)
	}
}

// goFieldInfo is everything assertTypeCompatible needs about a Go field.
type goFieldInfo struct {
	GoName string       // Go field name (for error messages)
	Kind   reflect.Kind // base Kind, with Ptr unwrapped
	IsSlice bool
	ElemKind reflect.Kind // only meaningful when IsSlice
}

// collectJSONTags returns a map from json tag name to goFieldInfo for
// every exported field of t. Embedded structs are flattened (matches
// json package's behaviour). Fields tagged `json:"-"` are skipped.
func collectJSONTags(t reflect.Type) map[string]goFieldInfo {
	out := map[string]goFieldInfo{}
	walkFields(t, out)
	return out
}

func walkFields(t reflect.Type, out map[string]goFieldInfo) {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		// Anonymous embedded struct: flatten its fields up to this
		// level, matching encoding/json semantics.
		if f.Anonymous {
			walkFields(f.Type, out)
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.SplitN(tag, ",", 2)[0]
		if name == "" {
			continue
		}
		info := goFieldInfo{GoName: f.Name, Kind: f.Type.Kind()}
		// Unwrap pointer: a *string is still a "string" on the wire.
		if info.Kind == reflect.Ptr {
			info.Kind = f.Type.Elem().Kind()
		}
		if info.Kind == reflect.Slice || info.Kind == reflect.Array {
			info.IsSlice = true
			info.ElemKind = f.Type.Elem().Kind()
			if info.ElemKind == reflect.Ptr {
				info.ElemKind = f.Type.Elem().Elem().Kind()
			}
		}
		out[name] = info
	}
}

// assertTypeCompatible checks that a Go field's reflect.Kind aligns
// with the swagger property's `type` string. We're deliberately
// permissive: int vs int64 both map to "integer" and we accept
// "string" for time.Time (swagger uses format:"date-time").
func assertTypeCompatible(t *testing.T, fieldName string, gf goFieldInfo, prop swaggerPropertyV2) {
	t.Helper()
	if prop.Type == "" && prop.Ref != "" {
		// Property is a $ref to another schema — likely an object/struct
		// composition. We don't recursively validate refs in V1; the
		// presence of the field is what matters for catching drift.
		return
	}

	wantSwaggerType := goKindToSwagger(gf.Kind)
	if gf.IsSlice {
		wantSwaggerType = "array"
	}

	if !assert.Equalf(t, wantSwaggerType, prop.Type,
		"field %q: Go kind %s maps to swagger type %q, but schema says %q",
		fieldName, gf.Kind, wantSwaggerType, prop.Type) {
		return
	}

	if gf.IsSlice && prop.Items != nil && prop.Items.Type != "" {
		wantElem := goKindToSwagger(gf.ElemKind)
		assert.Equalf(t, wantElem, prop.Items.Type,
			"field %q: Go slice element kind %s maps to swagger items.type %q, but schema says %q",
			fieldName, gf.ElemKind, wantElem, prop.Items.Type)
	}
}

func goKindToSwagger(k reflect.Kind) string {
	switch k {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Map, reflect.Struct:
		return "object"
	}
	return ""
}

// TestSwaggerVendorIntegrity is a smoke test that the vendored copy is
// parseable and non-trivial. A 0-byte or html-404 file would otherwise
// fail much later with confusing per-case errors.
func TestSwaggerVendorIntegrity(t *testing.T) {
	t.Parallel()
	s := loadSwagger(t)
	// Sanity floor — the real schema has hundreds of definitions; a
	// truncated vendor would dip below this.
	require.Greaterf(t, len(s.Definitions), 50,
		"vendored swagger.json has only %d definitions — likely truncated. Re-run refresh-swagger.sh.",
		len(s.Definitions))

	// Confirm the handlers we depend on are present. Drift here means
	// the backend renamed or removed a handler type without us noticing.
	want := []string{
		"handlers.CreateClusterRequest",
		"handlers.UpdateClusterRequest",
		"handlers.BulkOperationRequest",
		"handlers.BulkTemplateRequest",
		"handlers.RegisterRequest",
		"handlers.LoginRequest",
		"handlers.CreateAPIKeyRequest",
		"handlers.ResetPasswordRequest",
		"models.CleanupPolicy",
	}
	sort.Strings(want)
	for _, name := range want {
		_, ok := s.Definitions[name]
		assert.Truef(t, ok, "vendored swagger.json is missing definition %q — backend renamed or removed it?", name)
	}
}

