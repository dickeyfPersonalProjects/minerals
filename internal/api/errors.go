package api

import (
	"encoding/json"
	"net/http"
)

// errorEnvelope is the §10 mandatory error response shape.
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// writeError writes a §10-compliant error envelope at status. Internal
// diagnostic detail (stack traces, SQL fragments, paths) MUST NOT be
// included; logs are the place for those (per §17).
func writeError(w http.ResponseWriter, status int, code, msg string, details map[string]any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope{
		Error: errorBody{Code: code, Message: msg, Details: details},
	})
}
