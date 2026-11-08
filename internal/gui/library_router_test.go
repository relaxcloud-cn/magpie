package gui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/library"
	"github.com/yetone/magpie/internal/usage"
)

func routerGet(t *testing.T, query string) (routerJSON, string) {
	t.Helper()
	mux := http.NewServeMux()
	routerRoutes(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/library/router"+query, nil))
	if w.Code != 200 {
		t.Fatalf("%s: %d %s", query, w.Code, w.Body)
	}
	var v routerJSON
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v, w.Body.String()
}

// The router's state counts the calls it passed on since the asked time, by
// server, tool and caller, and counts only the router's own rows of servers
// the Library has.
func TestRouterCounts(t *testing.T) {
	sandboxHome(t)
	if _, err := library.SaveServer("", library.Server{Name: "docs", Transport: "http", URL: "http://user:pw-SECRET@127.0.0.1:9/mcp/docs?k=SECRET-q", Agents: []string{"claude"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := library.SaveServer("", library.Server{Name: "quiet", Transport: "http", URL: "http://127.0.0.1:9/mcp", Agents: []string{"claude"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := library.SetMCPHub(true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { library.SetMCPHub(false) })
	now := time.Now().Add(-time.Hour)
	call := func(at time.Time, server, tool, endpoint, keyID, keyName, failure string) {
		usage.Append(usage.Record{Time: at, Agent: "claude", Operation: "execute_tool", Requested: tool, Host: server, Error: failure,
			Endpoint: endpoint, Kind: usage.KindMCP, Source: usage.SourceMCP, CallerKeyID: keyID, CallerKeyName: keyName})
	}
	hub := "/mcp/magpie/hub/claude"
	call(now.Add(-72*time.Hour), "docs", "docs__search", hub, "", "", "") // before since
	call(now, "docs", "docs__search", hub, "", "", "")                    // local
	call(now.Add(time.Second), "docs", "docs__search", hub, "k1", "assistant", "")
	call(now.Add(2*time.Second), "docs", "docs__search", "/mcp/magpie/hub", "k1", "assistant", "it didn't answer")
	call(now.Add(3*time.Second), "docs", "docs__fetch", hub, "k2", "", "")                  // a key with no name: its id
	call(now.Add(4*time.Second), "docs", "docs__fetch", "/mcp/magpie/tools", "k1", "x", "") // not the router's
	call(now.Add(5*time.Second), "nope", "nope__a", hub, "", "", "")                        // not in the Library
	usage.Append(usage.Record{Time: now, Agent: "claude", Provider: "openai", Host: "api.openai.com", Model: "gpt-5", Requested: "gpt-5", Status: 200})

	since := now.Add(-time.Minute).UTC().Format(time.RFC3339)
	v, body := routerGet(t, "?since="+url.QueryEscape(since))
	if !v.On || v.Path != "/mcp/magpie/hub" || v.Since.UTC().Format(time.RFC3339) != since || len(v.Servers) != 2 {
		t.Fatalf("router %+v", v)
	}
	a, q := v.Servers[0], v.Servers[1]
	if a.Name != "docs" || a.Calls != (routerCount{4, 1}) {
		t.Fatalf("docs's calls %+v, want 4 with 1 failed", a.Calls)
	}
	if len(a.ByTool) != 2 || a.ByTool[0].Tool != "docs__search" || a.ByTool[0].routerCount != (routerCount{3, 1}) ||
		a.ByTool[1].Tool != "docs__fetch" || a.ByTool[1].routerCount != (routerCount{1, 0}) {
		t.Fatalf("by tool %+v", a.ByTool)
	}
	if len(a.ByCaller) != 3 || a.ByCaller[0].Caller != "assistant" || a.ByCaller[0].routerCount != (routerCount{2, 1}) ||
		a.ByCaller[1].Caller != "k2" || a.ByCaller[2].Caller != "local" {
		t.Fatalf("by caller %+v, want the key's name, its id when it has none, local for none", a.ByCaller)
	}
	if q.Name != "quiet" || q.Calls.Total != 0 || q.ByTool == nil || q.ByCaller == nil {
		t.Fatalf("quiet %+v", q)
	}
	if a.URL == nil || *a.URL != "http://127.0.0.1:9/mcp/docs" {
		t.Errorf("docs's address is not cut to scheme://host:port/path: %+v", a.RouterServer)
	}
	for _, secret := range []string{"pw-SECRET", "SECRET-q", "user:"} {
		if strings.Contains(body, secret) {
			t.Errorf("the answer tells %s: %s", secret, body)
		}
	}

	// a later since leaves the earlier calls out
	v, _ = routerGet(t, "?since="+url.QueryEscape(now.Add(1500*time.Millisecond).UTC().Format(time.RFC3339Nano)))
	if got := v.Servers[0].Calls; got != (routerCount{2, 1}) {
		t.Errorf("calls since a later time: %+v, want 2 with 1 failed", got)
	}

	// with no since it is the start of today here
	v, _ = routerGet(t, "")
	y, m, d := time.Now().Date()
	if want := time.Date(y, m, d, 0, 0, 0, 0, time.Local); !v.Since.Equal(want) {
		t.Errorf("default since %v, want %v", v.Since, want)
	}
	mux := http.NewServeMux()
	routerRoutes(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/library/router?since=yesterday", nil))
	if w.Code != http.StatusBadRequest {
		t.Errorf("a since that isn't a time: %d", w.Code)
	}
}

// With the router off the answer says so and counts nothing as served.
func TestRouterOff(t *testing.T) {
	sandboxHome(t)
	v, _ := routerGet(t, "")
	if v.On || v.Path != "/mcp/magpie/hub" || v.Servers == nil || len(v.Servers) != 0 {
		t.Fatalf("an off router with no servers: %+v", v)
	}
}
