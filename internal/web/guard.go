package web

import (
	"mime"
	"net/http"
	"strings"
)

// The site writes files and runs `go test`, so a request that changes anything
// has to be proved to come from one of this site's own pages.
//
// Binding to 127.0.0.1 does not provide that proof. Any page open in the same
// browser can POST to localhost, and a form-encoded POST is a CORS "simple
// request": no preflight is sent, so the request arrives and the side effect
// happens even though the attacker cannot read the response. Without the checks
// below, a page on another site could write Go source into solution.go and then
// ask this server to execute it.
//
// Sec-Fetch-Site is the primary check — browsers set it themselves and it cannot
// be forged by page script. Origin is the fallback for a client that does not
// send it. A request carrying neither is refused rather than trusted, which also
// means curl cannot reach these endpoints without passing a header explicitly.

// sameOrigin reports whether a state-changing request came from this site.
func sameOrigin(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin":
		return true
	case "":
		// Fall through to the Origin check below.
	default:
		// "cross-site", "same-site" and "none" are all refused: a mutation here
		// is only ever issued by this site's own pages.
		return false
	}

	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	// r.Host is what the client asked for, which is what Origin must match.
	// Comparing against the listen address would break the equivalent names
	// localhost and 127.0.0.1.
	host := strings.TrimPrefix(strings.TrimPrefix(origin, "http://"), "https://")
	return host == r.Host
}

// guard refuses a mutation that did not come from this site's own pages.
func guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			http.Error(w, "this endpoint only accepts requests from the study site itself",
				http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// guardJSON is guard plus a JSON body requirement, for the endpoints that write
// files and run tests.
//
// Demanding application/json is the second lock: it is not a CORS-simple content
// type, so a cross-origin attempt needs a preflight that this server never
// answers, and the request is stopped by the browser before it is sent.
func guardJSON(next http.HandlerFunc) http.HandlerFunc {
	return guard(func(w http.ResponseWriter, r *http.Request) {
		ct, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || ct != "application/json" {
			http.Error(w, "expected Content-Type: application/json", http.StatusUnsupportedMediaType)
			return
		}
		next(w, r)
	})
}
