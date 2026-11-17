// Package docs embeds the API specification so the binary can serve it and tests can
// check the server against it.
package docs

import _ "embed" // for //go:embed

// OpenAPI is the contents of openapi.yaml.
//
//go:embed openapi.yaml
var OpenAPI []byte
