package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func formatCall(t *testing.T, h http.Handler, slug, code string) formatResponse {
	t.Helper()
	rec := postJSON(t, h, "/problems/"+slug+"/format", map[string]string{"code": code})
	if rec.Code != http.StatusOK {
		t.Fatalf("format = %d: %s", rec.Code, rec.Body)
	}
	var out formatResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// The format key has to be safe to press on code that does not compile yet,
// which is most of the time it will be pressed.
func TestFormatTidiesGoAndKeepsUnparsableCode(t *testing.T) {
	srv, _, _ := testSite(t)
	h := srv.Handler()
	if rec := postJSON(t, h, "/problems/two-sum/create", map[string]any{}); rec.Code != 200 {
		t.Fatalf("create returned %d: %s", rec.Code, rec.Body)
	}

	out := formatCall(t, h, "two-sum", "package twosum\nfunc  f( ){\nreturn\n}\n")
	if out.Tool != "gofmt" {
		t.Errorf("tool = %q, want gofmt", out.Tool)
	}
	if !strings.Contains(out.Code, "func f() {") {
		t.Errorf("gofmt did not run:\n%s", out.Code)
	}
	if out.Error != "" || out.Why != "" {
		t.Errorf("unexpected error %q / why %q", out.Error, out.Why)
	}

	broken := "package twosum\nfunc f( {\n"
	out = formatCall(t, h, "two-sum", broken)
	if out.Error == "" {
		t.Error("a parse error was not reported")
	}
	if out.Code != broken {
		t.Errorf("unparsable code was changed:\n%s", out.Code)
	}
}

// Python has no formatter in this project, and saying nothing would look like
// a key that does nothing.
func TestFormatSaysWhenTheLanguageHasNoFormatter(t *testing.T) {
	srv, root, _ := testSite(t)
	writePySignature(t, root)
	h := srv.Handler()
	if rec := postJSON(t, h, "/problems/two-sum/start", map[string]string{"lang": "python"}); rec.Code != 200 {
		t.Fatalf("start = %d: %s", rec.Code, rec.Body)
	}

	src := "class Solution:\n    def twoSum(self,nums,target):\n        return  []\n"
	out := formatCall(t, h, "two-sum", src)
	if out.Why == "" {
		t.Error("no reason given for doing nothing")
	}
	if out.Tool != "" {
		t.Errorf("tool = %q, want none", out.Tool)
	}
	if out.Code != src {
		t.Errorf("Python source was changed:\n%s", out.Code)
	}
}

// Clearing the conversation is a rare move that sat as an equal button beside
// the one pressed every turn; it stays reachable, quietly.
func TestTheTutorActionRowHasOneButtonToPress(t *testing.T) {
	srv, _, _ := testSite(t)
	fakeTutor(t, srv, fakeTutorScript)
	h := srv.Handler()
	if rec := postJSON(t, h, "/problems/two-sum/create", map[string]any{}); rec.Code != 200 {
		t.Fatalf("create returned %d: %s", rec.Code, rec.Body)
	}
	body := get(t, h, "/problems/two-sum/solve").Body.String()
	if !strings.Contains(body, `class="quiet" data-tutor-reset`) {
		t.Error("start over is still an ordinary button beside Ask")
	}
	if !strings.Contains(body, "data-tutor-send") {
		t.Error("Ask is missing")
	}
}

// Python formatting is optional: the key reports what is missing rather than
// appearing to do nothing, and works as soon as a formatter is there.
func TestPythonFormattingIsOptionalAndUsesRuffWhenPresent(t *testing.T) {
	srv, root, _ := testSite(t)
	writePySignature(t, root)
	h := srv.Handler()
	if rec := postJSON(t, h, "/problems/two-sum/start", map[string]string{"lang": "python"}); rec.Code != 200 {
		t.Fatalf("start = %d: %s", rec.Code, rec.Body)
	}
	src := "class Solution:\n    def twoSum(self,nums,target):\n        return  []\n"

	// With no formatter on the machine the page is told what to install.
	srv.pyFormatTool = ""
	if out := formatCall(t, h, "two-sum", src); out.Why == "" || !strings.Contains(out.Why, "ruff") {
		t.Errorf("why = %q, want it to name ruff", out.Why)
	}

	// With one, the formatted source comes back and the tool is named.
	srv.pyFormatTool = "ruff"
	srv.pyFormat = func(_ context.Context, in string) (string, error) {
		return strings.ReplaceAll(in, "self,nums,target", "self, nums, target"), nil
	}
	out := formatCall(t, h, "two-sum", src)
	if out.Tool != "ruff" || !strings.Contains(out.Code, "self, nums, target") {
		t.Errorf("tool = %q code = %q", out.Tool, out.Code)
	}
	if !strings.Contains(get(t, h, "/problems/two-sum/solve").Body.String(), "ruff") {
		t.Error("the workspace does not advertise the key once a formatter exists")
	}

	// A formatter that refuses leaves the source alone and says why.
	srv.pyFormat = func(context.Context, string) (string, error) {
		return "", errors.New("error: expected an expression")
	}
	if out := formatCall(t, h, "two-sum", src); out.Error == "" || out.Code != src {
		t.Errorf("error = %q, code changed = %v", out.Error, out.Code != src)
	}
}
