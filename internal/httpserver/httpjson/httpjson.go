// Package httpjson provides the JSON response envelope shared by
// internal/httpserver and its middleware subpackage. It's split out so
// middleware can write responses (401, 429) without importing its
// parent package.
package httpjson

import (
	"encoding/json"
	"net/http"
)

func Write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func WriteError(w http.ResponseWriter, status int, message string) {
	Write(w, status, map[string]string{"error": message})
}
