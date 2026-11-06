package sdk

import _ "embed"

// OpenAPI is the spec of the HTTP API this client calls, for studios writing a
// client in another language.
//
//go:embed openapi.yaml
var OpenAPI []byte
