package specval

import (
	"errors"
	"fmt"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
)

// House problem+json codes for request-validation failures. Route mismatches are not encoded
// here: the caller passes those through to the generated mux, which owns 404/405.
const (
	codeInvalidBody  = "invalid_body"
	codeInvalidParam = "invalid_param"

	malformedJSONBody = "malformed JSON body"
)

// encode maps a validation error to a (code, detail) pair in the house problem+json voice.
// AuthenticationFunc is always a no-op and MultiError stays off, so in practice every error
// here is a *openapi3filter.RequestError; anything else takes the defensive fallback below.
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

	// Body failure with no nested schema error: a JSON parse failure, a body cut short by
	// http.MaxBytesReader, or an absent required body - none carry a field name, and all are
	// truthfully "malformed body".
	if !hasSchemaErr {
		return codeInvalidBody, malformedJSONBody
	}
	return codeInvalidBody, schemaPhrase(schemaErr, leafField(schemaErr.JSONPointer()))
}

// surfaceWrappedEnum digs the real cause out of an allOf mismatch when that cause is an enum
// violation: the allOf-wrapped single-$ref form lets a property keep a per-site description or
// default beside a shared schema, and kin-openapi reports any failure inside it as a generic
// "doesn't match all schemas" wrapper. Every other wrapped cause keeps that generic phrase.
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

// bindingPhrase covers parameter failures that never reach schema validation: an absent
// required parameter, or any other binding failure. Same phrasing family as schemaPhrase, keyed by field.
func bindingPhrase(reqErr *openapi3filter.RequestError, field string) string {
	if errors.Is(reqErr.Err, openapi3filter.ErrInvalidRequired) {
		return field + " is required"
	}
	return field + " is invalid"
}

// schemaPhrase renders a SchemaError as a house-voice sentence naming field; unmapped
// SchemaField values fall back to "field is invalid".
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

// characterCount renders a length bound with its noun, singular at one ("1 character"), plural otherwise.
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

// leafField picks the field name to blame from a SchemaError's JSON pointer path, walking
// from the end and skipping array indices. Falls back to "value" when no property is named.
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
