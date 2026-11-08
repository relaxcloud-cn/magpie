package usage

import "time"

// mcpSlack is how far before since VisitMCP keeps looking: a call's row is
// appended when the call ends, stamped with when it began, so the log is in
// time order to within the longest call (the router waits 50s for one).
const mcpSlack = 24 * time.Hour

// VisitMCP calls fn with each MCP tool call (SourceMCP) from since on that
// the user has not cleared, newest first. It reads the log's index in memory
// (the probes beside its blocks) and stops at the first row older than since
// by mcpSlack, so a day's counts cost the day's calls, not the history.
func VisitMCP(since time.Time, fn func(Record)) {
	readCleared()
	cleared := clearedNow()
	probes := readLogSnapshot().probes
	for i := len(probes) - 1; i >= 0; i-- {
		r := &probes[i].Record
		if !since.IsZero() && r.Time.Before(since.Add(-mcpSlack)) {
			return
		}
		if r.Source != SourceMCP || r.Time.Before(since) || cleared.hides(r) {
			continue
		}
		fn(*r)
	}
}
