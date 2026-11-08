package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/yetone/magpie/internal/access"
)

// The hub tells a server that asks for it who made a call from the gateway
// key the request was authorized with: its name, or nothing (the library
// says local) for a request with no key. A CallerHeader the caller sent
// is not that key: it changes nothing, with a key or without.
func TestHubCallerIsTheKeyNotAHeader(t *testing.T) {
	fresh(t)
	t.Setenv("MAGPIE_ADDR", "")
	t.Setenv("MAGPIE_PUBLIC_URL", "")
	t.Setenv("MAGPIE_TRUST_PROXY", "")
	var mu sync.Mutex
	var told []string
	f := &fakeHub{on: true,
		tools: map[string][]HubTool{"": {{Server: "search", Tool: "lookup", Spec: hubSpec("lookup")}}},
		call: func(ctx context.Context, server, tool string) (json.RawMessage, error) {
			mu.Lock()
			defer mu.Unlock()
			told = append(told, CallerName(HubCallerOf(ctx)))
			return json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`), nil
		}}
	withHub(t, f)
	h := lanGuard(New().Handler())
	_, secrets := newCaller(t, "assistant", "worker")
	const there = "192.0.2.7:4000"
	call := toolsCall("search"+hubSep+"lookup", `{}`)

	for _, c := range []struct {
		addr string
		hdr  []string
		want string
	}{
		{here, nil, LocalCaller},
		{here, []string{CallerHeader, "assistant"}, LocalCaller},
		{there, []string{"Authorization", "Bearer " + secrets[0]}, "assistant"},
		{there, []string{"Authorization", "Bearer " + secrets[1], CallerHeader, "assistant"}, "worker"},
		{there, []string{"Authorization", "Bearer " + secrets[0], CallerHeader, "local"}, "assistant"},
	} {
		if m := readMCP(t, hubPostAt(h, HubMCPAll, c.addr, call, c.hdr...)); m.Error != nil || m.Result.IsError {
			t.Fatalf("%s %v: %+v", c.addr, c.hdr, m)
		}
		mu.Lock()
		got := told[len(told)-1]
		mu.Unlock()
		if got != c.want {
			t.Errorf("%s %v: the server is told %q, want %q", c.addr, c.hdr, got, c.want)
		}
	}
}

// A key's name is one header value: control characters become spaces, it
// is cut at a character, and a key with no name is told by its id.
func TestCallerKeyName(t *testing.T) {
	long := strings.Repeat("密", 100)
	for _, c := range []struct {
		who  access.Identity
		want string
	}{
		{access.Identity{}, ""},
		{access.Identity{KeyID: "k1", KeyName: "assistant"}, "assistant"},
		{access.Identity{KeyID: "k1", KeyName: "研究助手"}, "研究助手"},
		{access.Identity{KeyID: "k1", KeyName: "a\r\nX-Evil: 1\tb"}, "a  X-Evil: 1 b"},
		{access.Identity{KeyID: "k1", KeyName: "  \r\n "}, "k1"},
		{access.Identity{KeyID: "k1"}, "k1"},
		{access.Identity{KeyID: "k1", KeyName: long}, strings.Repeat("密", maxCallerName/3)},
	} {
		if got := callerKeyName(c.who); got != c.want {
			t.Errorf("%+v: %q, want %q", c.who, got, c.want)
		}
	}
	if got := CallerName(HubCaller{}); got != LocalCaller {
		t.Errorf("no key: %q, want %q", got, LocalCaller)
	}
}
