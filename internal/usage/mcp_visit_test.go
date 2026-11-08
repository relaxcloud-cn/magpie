package usage

import (
	"testing"
	"time"

	"github.com/yetone/magpie/internal/settings"
)

// VisitMCP hands over the tool calls from since on, newest first, and only
// those: not the model requests beside them, not another probe, not a call
// the user cleared.
func TestVisitMCP(t *testing.T) {
	reportedHome(t)
	now := time.Now().Add(-time.Hour)
	call := func(at time.Time, tool string) Record {
		return Record{Time: at, Agent: "claude", Operation: "execute_tool", Requested: tool, Host: "docs",
			Endpoint: "/mcp/magpie/hub/claude", Kind: KindMCP, Source: SourceMCP}
	}
	Append(call(now.Add(-72*time.Hour), "docs__old"))
	Append(Record{Time: now, Agent: "claude", Provider: "openai", Host: "api.openai.com", Model: "gpt-5", Requested: "gpt-5", Endpoint: "/v1/responses", Status: 200})
	Append(Record{Time: now, Agent: "magpie", Provider: "openai", Host: "api.openai.com", Model: "gpt-5", Requested: "gpt-5", Kind: "test", Source: SourceProbe, Status: 200})
	Append(call(now.Add(time.Second), "docs__first"))
	Append(call(now.Add(2*time.Second), "docs__second"))

	var got []string
	VisitMCP(now.Add(-time.Minute), func(r Record) { got = append(got, r.Requested) })
	if len(got) != 2 || got[0] != "docs__second" || got[1] != "docs__first" {
		t.Fatalf("calls since an hour ago: %v, want the two hub calls, newest first", got)
	}
	got = nil
	VisitMCP(time.Time{}, func(r Record) { got = append(got, r.Requested) })
	if len(got) != 3 || got[2] != "docs__old" {
		t.Fatalf("every call: %v", got)
	}

	// what the user cleared from the usage pages is not counted here either
	if err := settings.ClearUsage("", "", now.Add(1500*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	got = nil
	VisitMCP(time.Time{}, func(r Record) { got = append(got, r.Requested) })
	if len(got) != 1 || got[0] != "docs__second" {
		t.Fatalf("calls after a clear: %v, want only the one after it", got)
	}
}
