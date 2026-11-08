package library

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/yetone/magpie/internal/gateway"
)

// callerSeen is the X-Magpie-Caller each request of a fake streamable HTTP
// server came with, by method.
type callerSeen struct {
	mu sync.Mutex
	by map[string][]string
}

func (c *callerSeen) of(method string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.by[method]...)
}

func callerServer(t *testing.T, seen *callerSeen) *httptest.Server {
	var hdr http.Header
	inner := mcpHandler(t, false, &hdr)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		var m struct{ Method string }
		json.Unmarshal(body, &m)
		seen.mu.Lock()
		if seen.by == nil {
			seen.by = map[string][]string{}
		}
		seen.by[m.Method] = append(seen.by[m.Method], r.Header.Get(gateway.CallerHeader))
		seen.mu.Unlock()
		inner(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A server that asked (Server.Caller) is told who calls it in
// X-Magpie-Caller on each call the router passes on: the gateway key's
// name, or "local" for a call with none. A server that didn't ask is sent
// nothing, and a header of that name among the server's own headers can't
// stand for the caller of one that did.
func TestHubTellsServerItsCaller(t *testing.T) {
	hubSandbox(t)
	var asked, plain callerSeen
	askedSrv, plainSrv := callerServer(t, &asked), callerServer(t, &plain)
	spoof := map[string]string{gateway.CallerHeader: "root"}
	ok(t)(SaveServer("", Server{Name: "asked", Transport: "http", URL: askedSrv.URL, Headers: spoof, Caller: true, Agents: []string{"claude"}}))
	ok(t)(SaveServer("", Server{Name: "plain", Transport: "http", URL: plainSrv.URL, Agents: []string{"claude"}}))
	ok(t)(SetMCPHub(true))
	hub := theHub{}
	ctx := context.Background()

	call := func(ctx context.Context, server string) {
		t.Helper()
		if res, err := hub.Call(ctx, "claude", server, "a", json.RawMessage(`{}`)); err != nil || !strings.HasPrefix(hubText(t, res), "remote ") {
			t.Fatalf("%s's a: %s %v", server, res, err)
		}
	}
	if got := hubNames(hub.Tools(gateway.WithHubCaller(ctx, gateway.HubCaller{Key: "assistant"}), "claude")); len(got) != 4 {
		t.Fatalf("the hub lists %v, want both servers' two tools", got)
	}
	keyed := gateway.WithHubCaller(ctx, gateway.HubCaller{Key: "assistant"})
	call(keyed, "asked")
	call(keyed, "plain")
	call(ctx, "asked")
	call(gateway.WithHubCaller(ctx, gateway.HubCaller{Key: "worker"}), "asked")

	if got, want := strings.Join(asked.of("tools/call"), ","), "assistant,local,worker"; got != want {
		t.Errorf("the server that asked was told %q, want %q (a key's name, local for none)", got, want)
	}
	if got := strings.Join(plain.of("tools/call"), ","); got != "" {
		t.Errorf("a server that didn't ask was told %q", got)
	}
	// only a call carries it: a list is for every caller at once
	if got := strings.Join(asked.of("tools/list"), ","); got != "root" {
		t.Errorf("the list was sent %q, want only the server's own header", got)
	}
}

// A server that asked over SSE is told too, in the POST of each call.
func TestHubTellsSSEServerItsCaller(t *testing.T) {
	hubSandbox(t)
	var posted []string
	var mu sync.Mutex
	events := make(chan string, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "event: endpoint\ndata: /messages\n\n")
			w.(http.Flusher).Flush()
			for {
				select {
				case e := <-events:
					io.WriteString(w, "event: message\ndata: "+e+"\n\n")
					w.(http.Flusher).Flush()
				case <-r.Context().Done():
					return
				}
			}
		}
		var m struct {
			ID     *int   `json:"id"`
			Method string `json:"method"`
		}
		json.NewDecoder(r.Body).Decode(&m)
		mu.Lock()
		if m.Method == "tools/call" {
			posted = append(posted, r.Header.Get(gateway.CallerHeader))
		}
		mu.Unlock()
		w.WriteHeader(202)
		switch m.Method {
		case "initialize":
			events <- `{"jsonrpc":"2.0","id":` + strconv.Itoa(*m.ID) + `,"result":{"protocolVersion":"2025-03-26","capabilities":{}}}`
		case "tools/list":
			events <- `{"jsonrpc":"2.0","id":` + strconv.Itoa(*m.ID) + `,"result":{"tools":[{"name":"a"}]}}`
		case "tools/call":
			events <- `{"jsonrpc":"2.0","id":` + strconv.Itoa(*m.ID) + `,"result":{"content":[{"type":"text","text":"remote sse"}]}}`
		}
	}))
	t.Cleanup(srv.Close)
	// the hub ends its event stream before the server is closed, which waits for it
	t.Cleanup(hubStopAll)
	ok(t)(SaveServer("", Server{Name: "live", Transport: "sse", URL: srv.URL, Caller: true, Agents: []string{"claude"}}))
	ok(t)(SetMCPHub(true))
	ctx := gateway.WithHubCaller(context.Background(), gateway.HubCaller{Key: "assistant"})
	if res, err := (theHub{}).Call(ctx, "claude", "live", "a", json.RawMessage(`{}`)); err != nil || hubText(t, res) != "remote sse" {
		t.Fatalf("live's a: %s %v", res, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(posted) != 1 || posted[0] != "assistant" {
		t.Errorf("the SSE server was told %q, want assistant", posted)
	}
}

// The opt-in is saved in library.json and kept by an edit that sends it,
// is turned on and off by ServerCaller, and is refused for a command
// server, which has no request to carry a header.
func TestServerCaller(t *testing.T) {
	hubSandbox(t)
	ok(t)(SaveServer("", Server{Name: "web", Transport: "http", URL: "https://example.com/mcp", Agents: []string{}}))
	if loaded(t).server("web").Caller {
		t.Fatal("a server asks for its caller by default")
	}
	ok(t)(ServerCaller("web", true))
	if !loaded(t).server("web").Caller {
		t.Fatal("ServerCaller on left it off")
	}
	// the editor sends the field back with the rest
	ok(t)(SaveServer("web", Server{Name: "web", Transport: "http", URL: "https://example.com/mcp", Caller: true, Agents: []string{}}))
	if !loaded(t).server("web").Caller {
		t.Error("a save that sends it turned it off")
	}
	ok(t)(ServerCaller("web", false))
	if loaded(t).server("web").Caller {
		t.Error("ServerCaller off left it on")
	}

	fake := fakeStdio(t, "ok", nil)
	fake.Agents = []string{}
	ok(t)(SaveServer("", *fake))
	if _, err := ServerCaller(fake.Name, true); err == nil || !strings.Contains(err.Error(), "URL server") {
		t.Errorf("a command server was told to send its caller: %v", err)
	}
	fake.Name, fake.Caller = "fake2", true
	ok(t)(SaveServer("", *fake))
	if loaded(t).server("fake2").Caller {
		t.Error("a command server was saved asking for its caller")
	}
	if _, err := ServerCaller("nope", true); err == nil {
		t.Error("a server not in the library was set")
	}
}
