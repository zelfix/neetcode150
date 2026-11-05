package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNotesCanBeEditedAndClearedInTheBrowser(t *testing.T) {
	srv, root, prog := testSite(t)
	h := srv.Handler()
	path := filepath.Join(root, "content/problems/0001-two-sum/notes.md")
	original, _ := os.ReadFile(path)
	for _, text := range []string{"---\ntitle: My notes\n---\n\n# Notes\nMy own explanation.\n</textarea><script>alert(1)</script>", ""} {
		rec := postJSON(t, h, "/problems/two-sum/notes", map[string]string{"text": text})
		if rec.Code != http.StatusOK {
			t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
		}
		textOnDB, err := prog.Scope().Document("two-sum", "notes")
		data := []byte(textOnDB)
		if err != nil || string(data) != text {
			t.Fatalf("notes not saved verbatim: %q, %v", data, err)
		}
		body := get(t, h, "/problems/two-sum/solve").Body.String()
		if !strings.Contains(body, "data-notes-form") || strings.Contains(body, "<script>alert(1)</script>") {
			t.Fatal("missing editor or unescaped notes")
		}
	}
	if legacy, _ := os.ReadFile(path); string(legacy) != string(original) {
		t.Fatal("legacy backup changed")
	}
	if rec := postJSON(t, h, "/problems/two-sum/notes", struct{}{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing text: %d", rec.Code)
	}
	if rec := postJSON(t, h, "/problems/unknown/notes", map[string]string{"text": "x"}); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown problem: %d", rec.Code)
	}
	if _, err := prog.StartAttempt("two-sum", srv.now()); err != nil {
		t.Fatal(err)
	}
	if _, err := prog.ResetTimer("two-sum", srv.now()); err != nil {
		t.Fatal(err)
	}
	if rec := postJSON(t, h, "/problems/two-sum/notes", map[string]string{"text": "during solve"}); rec.Code != http.StatusOK {
		t.Fatalf("active attempt: %d", rec.Code)
	}
	if body := get(t, h, "/problems/two-sum/solve").Body.String(); !strings.Contains(body, "data-notes-form") {
		t.Fatal("notes editor missing during attempt")
	}
}

func TestNotesRequireSameOriginJSON(t *testing.T) {
	srv, root, _ := testSite(t)
	path := filepath.Join(root, "content/problems/0001-two-sum/notes.md")
	before, _ := os.ReadFile(path)
	for _, site := range []string{"", "cross-site", "same-origin"} {
		req := signedRequest(httptest.NewRequest(http.MethodPost, "/problems/two-sum/notes", strings.NewReader(`{"text":"changed"}`)))
		req.Header.Set("Sec-Fetch-Site", site)
		if site != "same-origin" {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden && rec.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("unguarded write: %d", rec.Code)
		}
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("rejected request changed notes")
	}
}
