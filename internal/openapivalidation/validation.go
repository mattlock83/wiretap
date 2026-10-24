// Package openapivalidation keeps capture and offline validation on the same
// body decoders.
package openapivalidation

import (
	"context"

	"github.com/getkin/kin-openapi/openapi3filter"
)

func init() {
	// Express res.send(string), among other servers, returns text/html. Decode
	// the wire body as a string; the declared schema still validates its value.
	// Register during initialization because kin-openapi's registry is not
	// safe to mutate while requests are being validated.
	openapi3filter.RegisterBodyDecoder("text/html", openapi3filter.PlainBodyDecoder)
}

func ValidateRequest(ctx context.Context, input *openapi3filter.RequestValidationInput) error {
	return openapi3filter.ValidateRequest(ctx, input)
}

func ValidateResponse(ctx context.Context, input *openapi3filter.ResponseValidationInput) error {
	return openapi3filter.ValidateResponse(ctx, input)
}
