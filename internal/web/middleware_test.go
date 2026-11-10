package web

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A monitor has to be able to ask whether the site is up without a link.
func TestHealthAnswersWithoutACookie(t *testing.T) {
	srv, _, _ := testSite(t)
	rec := getAs(t, srv.Handler(), "/health", "")
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "ok" {
		t.Errorf("/health = %d %q", rec.Code, rec.Body.String())
	}
}

func TestEveryResponseCarriesTheSafetyHeaders(t *testing.T) {
	srv, _, _ := testSite(t)
	h := srv.Handler()
	for _, path := range []string{"/", "/health", "/static/app.css", "/nowhere"} {
		rec := getAs(t, h, path, testToken)
		if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options = %q", path, got)
		}
		if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
			t.Errorf("%s: X-Frame-Options = %q", path, got)
		}
	}
}

// A handler that panics should cost that one request a 500, not the
// connection, and the log line should say which request it was.
func TestAPanicBecomesA500AndIsLogged(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /boom", func(w http.ResponseWriter, r *http.Request) { panic("kaboom") })
	mux.HandleFunc("GET /fine", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := instrument(mux)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("panicking handler answered %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "kaboom") {
		t.Error("the panic value reached the browser")
	}
	if !strings.Contains(buf.String(), "kaboom") || !strings.Contains(buf.String(), "GET /boom 500") {
		t.Errorf("log does not name the panic and the request:\n%s", buf.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/fine", nil))
	if rec.Code != http.StatusNoContent {
		t.Errorf("the next request answered %d", rec.Code)
	}
	if !strings.Contains(buf.String(), "GET /fine 204") {
		t.Errorf("log line lacks the status:\n%s", buf.String())
	}
}

// The form endpoints take a few bytes; a body past maxBody is refused as such
// rather than read to the end and then reported as a missing field.
func TestAnOversizedFormIsRefused(t *testing.T) {
	srv, _, _ := testSite(t)
	body := "status=solved&pad=" + strings.Repeat("x", maxBody)
	rec := postFormAs(t, srv.Handler(), "/problems/two-sum/status", body, testToken, "same-origin")
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized form = %d, want 413", rec.Code)
	}
	rec = postFormAs(t, srv.Handler(), "/problems/two-sum/status", "status=solved", testToken, "same-origin")
	if rec.Code != http.StatusSeeOther {
		t.Errorf("ordinary form = %d, want 303", rec.Code)
	}
}
