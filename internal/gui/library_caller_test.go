package gui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A URL server saved with caller (the entrypoint of a deployment that
// wants its callers told, and the editor, which sends it back with the
// rest) is told who calls it through the router; the view says so, and
// a save that sends it off turns it off. A command server never is.
func TestServerCallerSaved(t *testing.T) {
	sandboxHome(t)
	h := Handler(nil, nil)
	save := func(body string) {
		t.Helper()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/library/servers/save", strings.NewReader(body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
	asked := func(name string) bool {
		t.Helper()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/library", nil))
		var v struct {
			Servers []struct {
				Name   string
				Caller bool
			}
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		for _, s := range v.Servers {
			if s.Name == name {
				return s.Caller
			}
		}
		t.Fatalf("no server %s in %s", name, rec.Body)
		return false
	}
	save(`{"server":{"name":"search","transport":"http","url":"http://search.internal:8080/mcp","agents":[],"caller":true}}`)
	if !asked("search") {
		t.Fatal("a server saved with caller isn't told its callers")
	}
	save(`{"old":"search","server":{"name":"search","transport":"http","url":"http://search.internal:8080/mcp","agents":[]}}`)
	if asked("search") {
		t.Fatal("a save that doesn't send caller left it on")
	}
	save(`{"server":{"name":"cmd","transport":"stdio","command":"/opt/x/mcp","agents":[],"caller":true}}`)
	if asked("cmd") {
		t.Fatal("a command server is told its callers")
	}
}
