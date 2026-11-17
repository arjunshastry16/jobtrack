package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// maxBodyBytes caps request bodies so a client can't exhaust memory with a huge upload.
const maxBodyBytes = 1 << 20 // 1 MiB

// writeJSON encodes v as the response body with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The status line is already sent, so an encode error can't change the response; ignore it.
	_ = json.NewEncoder(w).Encode(v)
}

// decodeJSON reads exactly one JSON object from the request body into dst. On failure it
// writes a 400/413 error response and returns false, so handlers can simply `return`.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields() // unknown fields (e.g. a typo) are errors, not silently ignored

	if err := dec.Decode(dst); err != nil {
		var tooBig *http.MaxBytesError
		switch {
		case errors.As(err, &tooBig):
			writeError(w, http.StatusRequestEntityTooLarge, codeBodyTooLarge,
				fmt.Sprintf("request body must be at most %d bytes", tooBig.Limit))
		case errors.Is(err, io.EOF):
			writeError(w, http.StatusBadRequest, codeInvalidJSON, "request body is empty")
		default:
			writeError(w, http.StatusBadRequest, codeInvalidJSON, "invalid JSON: "+err.Error())
		}
		return false
	}
	// Reject trailing content such as `{"name":"a"}{"name":"b"}`.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, codeInvalidJSON, "request body must contain a single JSON object")
		return false
	}
	return true
}
