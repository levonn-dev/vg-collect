package specval

import (
	"errors"
	"fmt"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
)

// House problem+json codes for request-validation failures. Route
// mismatches are not encoded here at all: the caller passes those
// straight through to the generated mux, which owns 404/405.
const (
	codeInvalidBody  = "invalid_body"
	codeInvalidParam = "invalid_param"

	malformedJSONBody = "malformed JSON body"
)

// encode maps a validation error from the request-validation middleware to
// a (code, detail) pair in the house problem+json voice. AuthenticationFunc
// is always the no-op (see specval.go) and MultiError stays off, so in
// practice every error reaching here is a *openapi3filter.RequestError;
// anything else takes the defensive fallback below.
func encode(err error) (code, detail string) {
	var reqErr *openapi3filter.RequestError
	if !errors.As(err, &reqErr) {
		return codeInvalidParam, "request is invalid"
	}

	var schemaErr *openapi3.SchemaError
	hasSchemaErr := errors.As(reqErr, &schemaErr)
	if hasSchemaErr {
		schemaErr = surfaceWrappedEnum(schemaErr)
	}

	if reqErr.Parameter != nil {
		field := reqErr.Parameter.Name
		if !hasSchemaErr {
			return codeInvalidParam, bindingPhrase(reqErr, field)
		}
		return codeInvalidParam, schemaPhrase(schemaErr, field)
	}

	// Body failure with no nested schema error: either a literal JSON
	// parse failure (decodeBody) or a body read cut short by
	// http.MaxBytesReader (surfaced as a RequestError wrapping
	// *http.MaxBytesError). Neither carries a field name, and both are
	// truthfully described as a malformed body, so both map to the same
	// house string. A required requestBody that is entirely absent
	// (RequestError wrapping ErrInvalidRequired, no SchemaError either)
	// lands here too, intentionally: there is no field to name, and "no
	// body" is just the degenerate case of "no parseable body".
	if !hasSchemaErr {
		return codeInvalidBody, malformedJSONBody
	}
	return codeInvalidBody, schemaPhrase(schemaErr, leafField(schemaErr.JSONPointer()))
}

// surfaceWrappedEnum digs the real cause out of an allOf mismatch when
// that cause is an enum violation. The allOf-wrapped single-$ref form is
// how a property keeps a per-site description or default beside a shared
// schema reference, and kin-openapi reports any failure inside it as a
// generic "doesn't match all schemas" wrapper; surfacing a wrapped enum
// error keeps the enum voice identical to an inline enum site's. Every
// other wrapped cause keeps the wrapper (and so the generic phrase):
// that is what allOf-wrapped string constraints have always answered,
// and the JSON pointer bookkeeping on the wrapper already names the
// right field either way.
func surfaceWrappedEnum(schemaErr *openapi3.SchemaError) *openapi3.SchemaError {
	if schemaErr.SchemaField != "allOf" {
		return schemaErr
	}
	var leaf *openapi3.SchemaError
	if errors.As(schemaErr.Origin, &leaf) && leaf.SchemaField == "enum" {
		return leaf
	}
	return schemaErr
}

// bindingPhrase covers parameter failures that never reach schema
// validation: a required parameter that is absent, or any other binding
// failure (empty-when-not-allowed, unparsable value, ...). It shares the
// same phrasing family as schemaPhrase, keyed by the parameter's own name.
func bindingPhrase(reqErr *openapi3filter.RequestError, field string) string {
	if errors.Is(reqErr.Err, openapi3filter.ErrInvalidRequired) {
		return field + " is required"
	}
	return field + " is invalid"
}

// schemaPhrase renders a SchemaError as a house-voice sentence naming
// field as the subject. The pattern/format/other bucket is the fallback
// for every SchemaField not called out above.
func schemaPhrase(schemaErr *openapi3.SchemaError, field string) string {
	switch schemaErr.SchemaField {
	case "required":
		return field + " is required"
	case "enum":
		return field + " must be one of " + joinEnum(schemaErr.Schema.Enum)
	case "maxLength":
		return field + " must be at most " + characterCount(*schemaErr.Schema.MaxLength)
	case "minLength":
		return field + " must be at least " + characterCount(schemaErr.Schema.MinLength)
	case "maxItems":
		return fmt.Sprintf("%s must be at most %d items", field, *schemaErr.Schema.MaxItems)
	case "minItems":
		return fmt.Sprintf("%s must be at least %d items", field, schemaErr.Schema.MinItems)
	default:
		return field + " is invalid"
	}
}

// characterCount renders a length bound with its noun, singular at one
// ("1 character") and plural otherwise ("2048 characters").
func characterCount(n uint64) string {
	if n == 1 {
		return "1 character"
	}
	return fmt.Sprintf("%d characters", n)
}

func joinEnum(values []any) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = fmt.Sprint(v)
	}
	return strings.Join(parts, ", ")
}

// leafField picks the field name to blame from a SchemaError's JSON
// pointer path, walking from the end and skipping array indices (e.g.
// "developers", "0" names the "developers" property, not its 0th item).
// A path of only indices, or no path at all, falls back to "value" (a
// body whose root schema is itself a scalar has no property to name).
func leafField(path []string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if !isArrayIndex(path[i]) {
			return path[i]
		}
	}
	return "value"
}

func isArrayIndex(segment string) bool {
	if segment == "" {
		return false
	}
	for _, r := range segment {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
