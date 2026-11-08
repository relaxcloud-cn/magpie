package gateway

import (
	"slices"
	"testing"
)

// The caller a router row is counted under is the one a server that asked
// is told: the key's name, its id when the name leaves nothing, local for
// no key.
func TestCallerLabel(t *testing.T) {
	for _, c := range []struct{ id, name, want string }{
		{"", "", LocalCaller},
		{"", "stale name", LocalCaller},
		{"k1", "assistant", "assistant"},
		{"k2", "", "k2"},
		{"k3", "two\nlines\t", "two lines"},
	} {
		if got := CallerLabel(c.id, c.name); got != c.want {
			t.Errorf("CallerLabel(%q, %q) = %q, want %q", c.id, c.name, got, c.want)
		}
	}
}

// HubToolNames names tools as the hub lists them to an agent.
func TestHubToolNames(t *testing.T) {
	got := HubToolNames([]HubTool{{Server: "docs", Tool: "search"}, {Server: "a b", Tool: "x.y"}, {Server: "a-b", Tool: "x-y"}})
	if want := []string{"docs__search", "a-b__x-y", "a-b__x-y_2"}; !slices.Equal(got, want) {
		t.Fatalf("names %v, want %v", got, want)
	}
}
