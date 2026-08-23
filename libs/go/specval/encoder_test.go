package specval

import (
	"errors"
	"net/http"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
)

// schemaErrFor runs value through schema's own JSON validation and returns
// the resulting *openapi3.SchemaError. Using the real kin-openapi
// validator (rather than hand-built SchemaError literals) is required:
// SchemaError's path bookkeeping is a private field only the validator
// itself populates, so this is the only way to construct a SchemaError
// whose JSONPointer() reflects real nesting.
func schemaErrFor(t *testing.T, schema *openapi3.Schema, value any) *openapi3.SchemaError {
	t.Helper()
	err := schema.VisitJSON(value)
	if err == nil {
		t.Fatal("value unexpectedly passed schema validation")
	}
	var schemaErr *openapi3.SchemaError
	if !errors.As(err, &schemaErr) {
		t.Fatalf("want *openapi3.SchemaError, got %T: %v", err, err)
	}
	return schemaErr
}

func uint64Ptr(v uint64) *uint64 { return &v }

func objectSchema(props map[string]*openapi3.SchemaRef, required []string) *openapi3.Schema {
	return &openapi3.Schema{Type: &openapi3.Types{"object"}, Properties: props, Required: required}
}

func TestEncode_BodySchemaRows(t *testing.T) {
	tests := []struct {
		name       string
		schema     *openapi3.Schema
		value      any
		wantDetail string
	}{
		{
			name: "maxLength",
			schema: objectSchema(map[string]*openapi3.SchemaRef{
				"name": {Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, MaxLength: uint64Ptr(3)}},
			}, nil),
			value:      map[string]any{"name": "toolong"},
			wantDetail: "name must be at most 3 characters",
		},
		{
			name: "minLength",
			schema: objectSchema(map[string]*openapi3.SchemaRef{
				"name": {Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, MinLength: 3}},
			}, nil),
			value:      map[string]any{"name": "ab"},
			wantDetail: "name must be at least 3 characters",
		},
		{
			name: "minLength of one is singular",
			schema: objectSchema(map[string]*openapi3.SchemaRef{
				"code": {Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, MinLength: 1}},
			}, nil),
			value:      map[string]any{"code": ""},
			wantDetail: "code must be at least 1 character",
		},
		{
			name: "maxLength of one is singular",
			schema: objectSchema(map[string]*openapi3.SchemaRef{
				"initial": {Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, MaxLength: uint64Ptr(1)}},
			}, nil),
			value:      map[string]any{"initial": "ab"},
			wantDetail: "initial must be at most 1 character",
		},
		{
			name: "maxItems",
			schema: objectSchema(map[string]*openapi3.SchemaRef{
				"developers": {Value: &openapi3.Schema{
					Type:     &openapi3.Types{"array"},
					MaxItems: uint64Ptr(2),
					Items:    &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
				}},
			}, nil),
			value:      map[string]any{"developers": []any{"a", "b", "c"}},
			wantDetail: "developers must be at most 2 items",
		},
		{
			name: "minItems",
			schema: objectSchema(map[string]*openapi3.SchemaRef{
				"tags": {Value: &openapi3.Schema{
					Type:     &openapi3.Types{"array"},
					MinItems: 2,
					Items:    &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}},
				}},
			}, nil),
			value:      map[string]any{"tags": []any{"a"}},
			wantDetail: "tags must be at least 2 items",
		},
		{
			name: "enum",
			schema: objectSchema(map[string]*openapi3.SchemaRef{
				"status": {Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Enum: []any{"active", "inactive"}}},
			}, nil),
			value:      map[string]any{"status": "bogus"},
			wantDetail: "status must be one of active, inactive",
		},
		{
			name:       "required",
			schema:     objectSchema(map[string]*openapi3.SchemaRef{"name": {Value: &openapi3.Schema{Type: &openapi3.Types{"string"}}}}, []string{"name"}),
			value:      map[string]any{},
			wantDetail: "name is required",
		},
		{
			name: "pattern falls back to the generic phrase",
			schema: objectSchema(map[string]*openapi3.SchemaRef{
				"code": {Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Pattern: "^[A-Z]+$"}},
			}, nil),
			value:      map[string]any{"code": "lower"},
			wantDetail: "code is invalid",
		},
		{
			name: "nested array item error is developers-anchored, not index-anchored",
			schema: objectSchema(map[string]*openapi3.SchemaRef{
				"developers": {Value: &openapi3.Schema{
					Type:  &openapi3.Types{"array"},
					Items: &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Enum: []any{"foss", "closed"}}},
				}},
			}, nil),
			value:      map[string]any{"developers": []any{"unknown"}},
			wantDetail: "developers must be one of foss, closed",
		},
		{
			name:       "scalar body root with no property path falls back to a generic subject",
			schema:     &openapi3.Schema{Type: &openapi3.Types{"string"}, MaxLength: uint64Ptr(3)},
			value:      "toolong",
			wantDetail: "value must be at most 3 characters",
		},
		{
			// The allOf-wrapped single-$ref form is how a property keeps a
			// per-site description or default beside a shared schema
			// reference. kin-openapi reports such a failure as a generic
			// allOf mismatch wrapping the real cause; an enum violation
			// inside the wrapper must still speak the enum voice, exactly
			// as it would at an inline enum site.
			name: "allOf-wrapped enum violation speaks the enum voice",
			schema: objectSchema(map[string]*openapi3.SchemaRef{
				"status": {Value: &openapi3.Schema{
					Default: "active",
					AllOf: openapi3.SchemaRefs{
						{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, Enum: []any{"active", "inactive"}}},
					},
				}},
			}, nil),
			value:      map[string]any{"status": "bogus"},
			wantDetail: "status must be one of active, inactive",
		},
		{
			// A non-enum failure inside an allOf wrapper keeps the generic
			// phrase: that is what allOf-wrapped string constraints (e.g.
			// the shared handle schema) have always answered, and changing
			// it here would change wire text at sites this encoder already
			// serves.
			name: "allOf-wrapped non-enum violation keeps the generic phrase",
			schema: objectSchema(map[string]*openapi3.SchemaRef{
				"name": {Value: &openapi3.Schema{
					AllOf: openapi3.SchemaRefs{
						{Value: &openapi3.Schema{Type: &openapi3.Types{"string"}, MinLength: 3}},
					},
				}},
			}, nil),
			value:      map[string]any{"name": "ab"},
			wantDetail: "name is invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schemaErr := schemaErrFor(t, tt.schema, tt.value)
			reqErr := &openapi3filter.RequestError{RequestBody: &openapi3.RequestBody{}, Err: schemaErr}
			code, detail := encode(reqErr)
			if code != "invalid_body" {
				t.Errorf("code = %q, want invalid_body", code)
			}
			if detail != tt.wantDetail {
				t.Errorf("detail = %q, want %q", detail, tt.wantDetail)
			}
		})
	}
}

func TestEncode_BodyNonSchemaFailuresAreMalformedJSON(t *testing.T) {
	tests := []struct {
		name string
		err  *openapi3filter.RequestError
	}{
		{
			name: "JSON syntax error while decoding the body",
			err: &openapi3filter.RequestError{
				RequestBody: &openapi3.RequestBody{},
				Reason:      "failed to decode request body",
				Err:         errors.New("invalid character 'x' looking for beginning of value"),
			},
		},
		{
			name: "body read cut short by http.MaxBytesReader",
			err: &openapi3filter.RequestError{
				RequestBody: &openapi3.RequestBody{},
				Reason:      "reading failed",
				Err:         &http.MaxBytesError{Limit: 1024},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, detail := encode(tt.err)
			if code != "invalid_body" {
				t.Errorf("code = %q, want invalid_body", code)
			}
			if detail != "malformed JSON body" {
				t.Errorf("detail = %q, want %q", detail, "malformed JSON body")
			}
		})
	}
}

func TestEncode_ParamRows(t *testing.T) {
	pageParam := &openapi3.Parameter{Name: "page", In: "query"}

	t.Run("schema enum failure uses the parameter name", func(t *testing.T) {
		statusParam := &openapi3.Parameter{Name: "status", In: "query"}
		schema := &openapi3.Schema{Type: &openapi3.Types{"string"}, Enum: []any{"active", "inactive"}}
		schemaErr := schemaErrFor(t, schema, "bogus")
		reqErr := &openapi3filter.RequestError{Parameter: statusParam, Err: schemaErr}

		code, detail := encode(reqErr)
		if code != "invalid_param" {
			t.Errorf("code = %q, want invalid_param", code)
		}
		if want := "status must be one of active, inactive"; detail != want {
			t.Errorf("detail = %q, want %q", detail, want)
		}
	})

	t.Run("schema range failure falls back to the generic phrase", func(t *testing.T) {
		max := 100.0
		schema := &openapi3.Schema{Type: &openapi3.Types{"integer"}, Max: &max}
		schemaErr := schemaErrFor(t, schema, float64(999))
		reqErr := &openapi3filter.RequestError{Parameter: pageParam, Err: schemaErr}

		code, detail := encode(reqErr)
		if code != "invalid_param" {
			t.Errorf("code = %q, want invalid_param", code)
		}
		if want := "page is invalid"; detail != want {
			t.Errorf("detail = %q, want %q", detail, want)
		}
	})

	t.Run("missing required parameter", func(t *testing.T) {
		reqErr := &openapi3filter.RequestError{
			Parameter: pageParam,
			Reason:    openapi3filter.ErrInvalidRequired.Error(),
			Err:       openapi3filter.ErrInvalidRequired,
		}

		code, detail := encode(reqErr)
		if code != "invalid_param" {
			t.Errorf("code = %q, want invalid_param", code)
		}
		if want := "page is required"; detail != want {
			t.Errorf("detail = %q, want %q", detail, want)
		}
	})

	t.Run("binding failure that is not a missing-required case falls back to the generic phrase", func(t *testing.T) {
		reqErr := &openapi3filter.RequestError{
			Parameter: pageParam,
			Reason:    openapi3filter.ErrInvalidEmptyValue.Error(),
			Err:       openapi3filter.ErrInvalidEmptyValue,
		}

		code, detail := encode(reqErr)
		if code != "invalid_param" {
			t.Errorf("code = %q, want invalid_param", code)
		}
		if want := "page is invalid"; detail != want {
			t.Errorf("detail = %q, want %q", detail, want)
		}
	})
}

func TestIsArrayIndex(t *testing.T) {
	tests := []struct {
		segment string
		want    bool
	}{
		{segment: "0", want: true},
		{segment: "12", want: true},
		{segment: "developers", want: false},
		// A JSONPointer segment is never empty in practice (object keys
		// are non-empty, array indices are always digit strings), but an
		// empty segment must not vacuously count as an index - an empty
		// range loop would otherwise report true with nothing checked.
		{segment: "", want: false},
	}
	for _, tt := range tests {
		if got := isArrayIndex(tt.segment); got != tt.want {
			t.Errorf("isArrayIndex(%q) = %v, want %v", tt.segment, got, tt.want)
		}
	}
}

func TestEncode_UnrecognizedErrorTypeFallsBackSafely(t *testing.T) {
	// AuthenticationFunc is always the no-op and MultiError stays off (see
	// specval.go), so in real use encode only ever sees *RequestError.
	// This proves the defensive fallback still returns a well-formed pair
	// rather than panicking if that invariant is ever broken upstream.
	code, detail := encode(errors.New("boom"))
	if code != "invalid_param" {
		t.Errorf("code = %q, want invalid_param", code)
	}
	if detail != "request is invalid" {
		t.Errorf("detail = %q, want %q", detail, "request is invalid")
	}
}
