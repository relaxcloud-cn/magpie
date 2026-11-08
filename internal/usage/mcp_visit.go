package usage

import "time"

// VisitMCP calls fn with each MCP tool call (SourceMCP) from since on that
// the user has not cleared, newest first. It reads the log's probe blocks
// (logSnapshot.probes, where tool calls are kept beside magpie's own
// requests), and eachProbe passes over every block that ends before since,
// so a day's counts cost the day's calls, not the history.
func VisitMCP(since time.Time, fn func(Record)) {
	readCleared()
	cleared := clearedNow()
	var calls []Record
	eachProbe(readLogSnapshot().probes, since, func(r Record, _ int64) {
		if r.Source == SourceMCP && !cleared.hides(&r) {
			calls = append(calls, r)
		}
	})
	for i := len(calls) - 1; i >= 0; i-- {
		fn(calls[i])
	}
}
