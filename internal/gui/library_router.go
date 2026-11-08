package gui

import (
	"cmp"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/library"
	"github.com/yetone/magpie/internal/usage"
)

// routerCount is a number of the router's tool calls and how many of them
// failed.
type routerCount struct {
	Total  int `json:"total"`
	Failed int `json:"failed"`
}

type routerByTool struct {
	// Tool is the router's name for the tool (<server>__<tool>), as the row
	// of the request log has it
	Tool string `json:"tool"`
	routerCount
}

type routerByCaller struct {
	// Caller is the gateway key's name, or "local" for a call with no key
	Caller string `json:"caller"`
	routerCount
}

type routerServerJSON struct {
	library.RouterServer
	Calls    routerCount      `json:"calls"`
	ByTool   []routerByTool   `json:"byTool"`
	ByCaller []routerByCaller `json:"byCaller"`
}

type routerJSON struct {
	On      bool               `json:"on"`
	Path    string             `json:"path"`
	Since   time.Time          `json:"since"`
	Servers []routerServerJSON `json:"servers"`
}

// routerSince is the start of the period the router's counts are of: the
// query's RFC 3339 time, else the start of today in this computer's time.
func routerSince(q string, now time.Time) (time.Time, error) {
	if q == "" {
		y, m, d := now.Date()
		return time.Date(y, m, d, 0, 0, 0, 0, now.Location()), nil
	}
	t, err := time.Parse(time.RFC3339, q)
	if err != nil {
		return time.Time{}, errors.New("since is a time like 2006-01-02T15:04:05Z07:00")
	}
	return t, nil
}

// routerRead is the router's state and the calls it passed on since since.
// The counts are read from the request log's index in memory (usage.VisitMCP),
// for the servers the Library has, never for a row the router didn't write.
func routerRead(since time.Time) (routerJSON, error) {
	view, err := library.ReadRouter()
	if err != nil {
		return routerJSON{}, err
	}
	out := routerJSON{On: view.On, Path: view.Path, Since: since, Servers: make([]routerServerJSON, len(view.Servers))}
	at := map[string]int{}
	tools := make([]map[string]*routerCount, len(view.Servers))
	callers := make([]map[string]*routerCount, len(view.Servers))
	for i, s := range view.Servers {
		out.Servers[i] = routerServerJSON{RouterServer: s, ByTool: []routerByTool{}, ByCaller: []routerByCaller{}}
		at[s.Name] = i
		tools[i], callers[i] = map[string]*routerCount{}, map[string]*routerCount{}
	}
	count := func(c *routerCount, failed bool) {
		c.Total++
		if failed {
			c.Failed++
		}
	}
	usage.VisitMCP(since, func(r usage.Record) {
		// the router's own rows: not the image, vision, search or plugin
		// tools' servers, whose rows are of other endpoints
		if r.Computer != "" || r.Endpoint != gateway.HubMCPAll && !strings.HasPrefix(r.Endpoint, gateway.HubMCPPath) {
			return
		}
		i, ok := at[r.Host]
		if !ok {
			return
		}
		failed := r.Failed()
		count(&out.Servers[i].Calls, failed)
		who := gateway.CallerLabel(r.CallerKeyID, r.CallerKeyName)
		for _, m := range []struct {
			by  map[string]*routerCount
			key string
		}{{tools[i], r.Requested}, {callers[i], who}} {
			c := m.by[m.key]
			if c == nil {
				c = &routerCount{}
				m.by[m.key] = c
			}
			count(c, failed)
		}
	})
	for i := range out.Servers {
		s := &out.Servers[i]
		for k, c := range tools[i] {
			s.ByTool = append(s.ByTool, routerByTool{Tool: k, routerCount: *c})
		}
		for k, c := range callers[i] {
			s.ByCaller = append(s.ByCaller, routerByCaller{Caller: k, routerCount: *c})
		}
		slices.SortFunc(s.ByTool, func(a, b routerByTool) int {
			return cmp.Or(cmp.Compare(b.Total, a.Total), strings.Compare(a.Tool, b.Tool))
		})
		slices.SortFunc(s.ByCaller, func(a, b routerByCaller) int {
			return cmp.Or(cmp.Compare(b.Total, a.Total), strings.Compare(a.Caller, b.Caller))
		})
	}
	return out, nil
}

// routerRoutes is the router's read-only state. It is the page's and the
// platform's view of what the MCP router serves; it starts and asks nothing.
func routerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/library/router", func(rw http.ResponseWriter, r *http.Request) {
		since, err := routerSince(r.URL.Query().Get("since"), time.Now())
		if err != nil {
			fail(rw, err)
			return
		}
		v, err := routerRead(since)
		if err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, v)
	})
}
