package server

import (
	"net/http"
	"strings"
)

// requestHeader must accompany every state-changing API call. Browsers cannot
// attach a custom header to a cross-site form post or a simple cross-origin
// fetch without a CORS preflight, which this server never grants, so the
// header alone blocks cross-site request forgery.
const requestHeader = "X-Requested-With"
const requestHeaderValue = "PMBattle"

func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			mutating := r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions
			if mutating && r.Header.Get(requestHeader) != requestHeaderValue {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "request is missing the PMBattle request header"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
