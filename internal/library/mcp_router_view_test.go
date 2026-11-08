package library

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/gateway"
)

func routerServerNamed(t *testing.T, v RouterView, name string) RouterServer {
	t.Helper()
	for _, s := range v.Servers {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no server %s in %+v", name, v.Servers)
	return RouterServer{}
}

func readRouter(t *testing.T) RouterView {
	t.Helper()
	v, err := ReadRouter()
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// routerText is a string the view may leave out, for a message.
func routerText(p *string) string {
	if p == nil {
		return "<null>"
	}
	return *p
}

// closedAddr is an address nothing listens on.
func closedAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

// With the router off it reads as off, lists the Library's servers as the
// router knows nothing of them, and starts none.
func TestRouterViewOff(t *testing.T) {
	hubSandbox(t)
	var seen http.Header
	srv := httptest.NewServer(mcpHandler(t, false, &seen))
	t.Cleanup(srv.Close)
	ok(t)(SaveServer("", Server{Name: "web", Transport: "http", URL: srv.URL, Agents: []string{"claude"}}))

	v := readRouter(t)
	if v.On || v.Path != "/mcp/magpie/hub" {
		t.Fatalf("off router read as %+v", v)
	}
	s := routerServerNamed(t, v, "web")
	if s.State != RouterUnknown || len(s.Tools) != 0 || s.Error != nil || s.ToolsCachedAt != nil || s.Title != nil {
		t.Fatalf("a server the router never reached: %+v", s)
	}
	hubPool.Lock()
	held := len(hubPool.m)
	hubPool.Unlock()
	if held != 0 {
		t.Fatalf("reading the router started %d servers", held)
	}
}

// A server is unknown until the router reaches it, running while its
// connection is up, idle once it is ended with its tools kept, and failed
// with why when it died or couldn't be reached; its tools are the router's
// card, named as an agent is given them, with those turned off marked.
func TestRouterViewStates(t *testing.T) {
	hubSandbox(t)
	var seen http.Header
	srv := httptest.NewServer(mcpHandler(t, false, &seen))
	t.Cleanup(srv.Close)
	fake := fakeStdio(t, "ok", nil)
	fake.Agents = []string{"claude"}
	fake.DisabledTools = []string{"b"}
	ok(t)(SaveServer("", *fake))
	ok(t)(SaveServer("", Server{Name: "web", Transport: "http", URL: srv.URL + "/mcp", Agents: []string{"claude"}, Caller: true, Local: true}))
	ok(t)(SaveServer("", Server{Name: "gone", Transport: "http", URL: "http://" + closedAddr(t) + "/mcp", Agents: []string{"claude"}}))
	ok(t)(SaveServer("", Server{Name: "kept", Transport: "http", URL: srv.URL, Hub: HubDirect, Agents: []string{"claude"}}))
	ok(t)(SetMCPHub(true))
	hub := theHub{}
	ctx := context.Background()

	v := readRouter(t)
	if !v.On {
		t.Fatal("the router reads as off")
	}
	for _, s := range v.Servers {
		if s.State != RouterUnknown {
			t.Errorf("%s is %s before the router reached it", s.Name, s.State)
		}
	}
	if got := fmt.Sprint(len(v.Servers), routerServerNamed(t, v, "kept").Shared, routerServerNamed(t, v, "web").Shared); got != "4 false true" {
		t.Errorf("servers, kept shared, web shared: %s", got)
	}

	hub.Tools(ctx, "claude")
	v = readRouter(t)
	f, w, g := routerServerNamed(t, v, "fake"), routerServerNamed(t, v, "web"), routerServerNamed(t, v, "gone")
	if f.State != RouterRunning || w.State != RouterRunning || f.Error != nil {
		t.Fatalf("listed servers: fake %s, web %s", f.State, w.State)
	}
	if f.ToolsCachedAt == nil || f.ToolsCachedAt.IsZero() {
		t.Error("a listed server has no time its tools were learnt")
	}
	var names []string
	for _, tool := range f.Tools {
		names = append(names, fmt.Sprintf("%s/%s/%v", tool.Name, tool.Tool, tool.Disabled))
	}
	if got, want := strings.Join(names, " "), "fake__a/a/false fake__b/b/true fake__c/c/false"; got != want {
		t.Errorf("fake's tools %q, want %q (b is turned off in the Library)", got, want)
	}
	if len(w.Tools) != 2 || w.Tools[0].Name != "web__a" || w.Tools[1].Disabled {
		t.Errorf("web's tools %+v", w.Tools)
	}
	if !w.Caller || !w.Local || !w.Shared || w.Transport != "http" || w.URL == nil || !strings.HasSuffix(*w.URL, "/mcp") {
		t.Errorf("web's flags %+v", w)
	}
	if g.State != RouterFailed || g.Error == nil || *g.Error == "" {
		t.Errorf("a server nothing listens for: %+v", g)
	}

	// ended with its tools kept: idle, and reading it starts nothing
	hubPool.Lock()
	held := hubPool.m["fake"]
	hubPool.Unlock()
	held.mu.Lock()
	c := held.conn
	held.conn = nil
	held.mu.Unlock()
	if c != nil {
		c.close()
	}
	if s := routerServerNamed(t, readRouter(t), "fake"); s.State != RouterIdle || len(s.Tools) != 3 {
		t.Fatalf("an ended server: %s with %d tools", s.State, len(s.Tools))
	}

	// a server that died says how, until it answers again
	if _, err := hub.Call(ctx, "claude", "fake", "die", json.RawMessage(`{}`)); err == nil {
		t.Fatal("a server that died answered")
	}
	s := routerServerNamed(t, readRouter(t), "fake")
	if s.State != RouterFailed || s.Error == nil || !strings.Contains(*s.Error, "exited (5)") {
		t.Fatalf("a server that died: %s %v", s.State, s.Error)
	}
	if _, err := hub.Call(ctx, "claude", "fake", "a", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if s := routerServerNamed(t, readRouter(t), "fake"); s.State != RouterRunning || s.Error != nil {
		t.Fatalf("a server that answered again: %s %v", s.State, s.Error)
	}

	ok(t)(SetMCPHub(false))
	if v := readRouter(t); v.On || routerServerNamed(t, v, "web").State != RouterIdle {
		t.Errorf("turned off: %+v", v)
	}
}

// A server's headers, variables, arguments, command, sign-in secret and the
// user, password and query of its address are in nothing the view tells,
// not in its address, nor in the error a failed start left.
func TestRouterViewLeaksNothing(t *testing.T) {
	hubSandbox(t)
	addr := closedAddr(t)
	secrets := []string{"hdr-SECRET-h", "key-SECRET-k", "env-SECRET-e", "arg-SECRET-a", "pw-SECRET-p", "usr-SECRET-u", "qry-SECRET-q", "frag-SECRET-f", "oauth-SECRET-o", "leakcmd-SECRET-c"}
	ok(t)(SaveServer("", Server{Name: "leaky", Transport: "http", Agents: []string{"claude"},
		URL:     "http://usr-SECRET-u:pw-SECRET-p@" + addr + "/mcp/p?token=qry-SECRET-q#frag-SECRET-f",
		Headers: map[string]string{"Authorization": "Bearer hdr-SECRET-h", "X-Api-Key": "key-SECRET-k"},
		Env:     map[string]string{"E": "env-SECRET-e"},
		OAuth:   &OAuthApp{ClientID: "id", ClientSecret: "oauth-SECRET-o"}}))
	ok(t)(SaveServer("", Server{Name: "prog", Transport: "stdio", Agents: []string{"claude"},
		Command: "/nonexistent/dir/leakcmd-SECRET-c", Args: []string{"--token", "arg-SECRET-a"}, Env: map[string]string{"TOKEN": "env-SECRET-e"}}))
	ok(t)(SetMCPHub(true))
	theHub{}.Tools(context.Background(), "claude")

	v := readRouter(t)
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range secrets {
		if strings.Contains(string(b), s) {
			t.Errorf("the view tells %s: %s", s, b)
		}
	}
	leaky, prog := routerServerNamed(t, v, "leaky"), routerServerNamed(t, v, "prog")
	if leaky.URL == nil || *leaky.URL != "http://"+addr+"/mcp/p" {
		t.Errorf("the address is %q, want scheme://host:port/path only", routerText(leaky.URL))
	}
	if leaky.State != RouterFailed || prog.State != RouterFailed || prog.Error == nil || prog.URL != nil {
		t.Errorf("leaky %+v, prog %+v: both fail, and a command has no address", leaky, prog)
	}
	if home, err := os.UserHomeDir(); err == nil && strings.Contains(string(b), home) {
		t.Errorf("the view tells the home folder %s", home)
	}
}

// routerError takes a failure's secrets and folders out wherever they are
// in it, and cuts an address in it to scheme://host/path.
func TestRouterError(t *testing.T) {
	home, _ := os.UserHomeDir()
	s := &Server{Name: "x", Transport: "http", URL: "https://u:pw-SECRET-p@host.example:8443/mcp?token=qry-SECRET-q",
		Headers: map[string]string{"Authorization": "Bearer hdr-SECRET-h"}, Env: map[string]string{"K": "env-SECRET-e"}, Args: []string{"arg-SECRET-a"}}
	msg := `Post "https://u:pw-SECRET-p@host.example:8443/mcp?token=qry-SECRET-q": sent hdr-SECRET-h and env-SECRET-e, ran arg-SECRET-a in ` + home + `/work`
	got := routerError(s, msg)
	for _, secret := range []string{"pw-SECRET-p", "qry-SECRET-q", "hdr-SECRET-h", "env-SECRET-e", "arg-SECRET-a", "token="} {
		if strings.Contains(got, secret) {
			t.Errorf("%q still has %s", got, secret)
		}
	}
	if !strings.Contains(got, "https://host.example:8443/mcp") {
		t.Errorf("%q lost the address", got)
	}
	if len([]rune(routerError(s, strings.Repeat("long ", 200)))) > maxRouterError+1 {
		t.Error("a long error isn't cut")
	}
}

// The names the view gives are the router's own, so a count by tool matches
// the request log's rows.
func TestRouterViewNamesAreTheRouters(t *testing.T) {
	hubSandbox(t)
	fake := fakeStdio(t, "ok", nil)
	fake.Agents = []string{"claude"}
	ok(t)(SaveServer("", *fake))
	ok(t)(SetMCPHub(true))
	ts := theHub{}.Tools(context.Background(), "claude")
	want := gateway.HubToolNames(ts)
	var got []string
	for _, tool := range routerServerNamed(t, readRouter(t), "fake").Tools {
		got = append(got, tool.Name)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("view names %v, the router's %v", got, want)
	}
}
