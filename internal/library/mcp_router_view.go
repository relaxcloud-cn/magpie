package library

// What the router shows of itself to a reader of its state (GET
// /api/library/router): which Library servers sit behind it, what each one's
// backend is doing, and the tools the router last learnt of it. It is read
// from what the router already keeps (hubPool, the cards, library.json), starts
// no server and asks none, and it is made to be shown to a person who may not
// be the one who set the servers up: no header, variable, argument, command,
// sign-in or home folder of a server is in it (routerError, redactURL).

import (
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/lazyre"
)

// Router states of one server: Running has a live connection, Idle has none
// (not started yet, or ended after hubIdle) but the router knows its tools,
// Failed is that its last start, list or call got no answer, Unknown is that
// the router never reached it and knows nothing of it.
const (
	RouterRunning = "running"
	RouterIdle    = "idle"
	RouterFailed  = "failed"
	RouterUnknown = "unknown"
)

// maxRouterError is the longest error the view tells, in characters.
const maxRouterError = 200

// RouterTool is one tool of a server behind the router. Name is what an
// agent calls it (<server>__<tool>), Tool the server's own name for it.
type RouterTool struct {
	Name        string `json:"name"`
	Tool        string `json:"tool"`
	Description string `json:"description"`
	Disabled    bool   `json:"disabled"`
}

// RouterServer is one Library server as the router's state shows it.
type RouterServer struct {
	Name      string  `json:"name"`
	Transport string  `json:"transport"`
	URL       *string `json:"url"`
	Caller    bool    `json:"caller"`
	// Shared is that the server is behind the router (Server.Hub is not
	// direct); Local that the library keeps it to this computer.
	Shared        bool         `json:"shared"`
	Local         bool         `json:"local"`
	Title         *string      `json:"title"`
	Says          *string      `json:"says"`
	State         string       `json:"state"`
	Error         *string      `json:"error"`
	ToolsCachedAt *time.Time   `json:"toolsCachedAt"`
	Tools         []RouterTool `json:"tools"`
}

// RouterView is the router's state: the switch, where it is served, and the
// Library's servers by name.
type RouterView struct {
	On      bool           `json:"on"`
	Path    string         `json:"path"`
	Servers []RouterServer `json:"servers"`
}

// ReadRouter is the router's state from library.json, the hub's pool and the
// cards. It never starts a server or opens a connection. A library that can't
// be read is an error, not an empty router.
func ReadRouter() (RouterView, error) {
	mu.Lock()
	l, err := load()
	mu.Unlock()
	if err != nil {
		return RouterView{}, err
	}
	v := RouterView{On: l.MCPHub, Path: gateway.HubMCPAll, Servers: []RouterServer{}}
	servers := slices.Clone(l.MCP)
	sort.SliceStable(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })

	// The names an agent is given are made over the shared servers' tools
	// together (a name two share is told apart by a number), as the hub's
	// list is; a server kept direct is named by itself.
	var pool []gateway.HubTool
	cardsOf := make([]*hubCard, len(servers))
	for i, s := range servers {
		cardsOf[i] = cardOf(s)
		if cardsOf[i] != nil && inHub(s, "") {
			pool = append(pool, enabledTools(s, cardsOf[i])...)
		}
	}
	pooled := gateway.HubToolNames(pool)
	next := 0
	for i, s := range servers {
		out := RouterServer{
			Name: s.Name, Transport: s.Transport, Caller: s.Caller, Shared: inHub(s, ""), Local: s.Local,
			Tools: []RouterTool{},
		}
		if s.Plugin != "" {
			out.Transport = "plugin"
		} else if s.Remote() {
			out.URL = redactURL(s.URL)
		}
		c := cardsOf[i]
		if c != nil {
			out.Title, out.Says = textOrNil(c.Title), textOrNil(c.Says)
			var names []string
			if out.Shared {
				names = pooled[next : next+len(enabledTools(s, c))]
				next += len(names)
			} else {
				names = gateway.HubToolNames(enabledTools(s, c))
			}
			on := 0
			for _, t := range c.Tools {
				rt := RouterTool{Tool: t.Name, Description: t.Description, Disabled: slices.Contains(s.DisabledTools, t.Name)}
				if rt.Disabled {
					rt.Name = gateway.HubToolNames([]gateway.HubTool{{Server: s.Name, Tool: t.Name}})[0]
				} else {
					rt.Name, on = names[on], on+1
				}
				out.Tools = append(out.Tools, rt)
			}
		}
		out.State, out.Error, out.ToolsCachedAt = routerState(s, c)
		v.Servers = append(v.Servers, out)
	}
	return v, nil
}

// enabledTools are the tools of s the card lists that the Library hasn't
// turned off, as the hub lists them.
func enabledTools(s *Server, c *hubCard) []gateway.HubTool {
	var out []gateway.HubTool
	for _, t := range c.Tools {
		if !slices.Contains(s.DisabledTools, t.Name) {
			out = append(out, gateway.HubTool{Server: s.Name, Tool: t.Name})
		}
	}
	return out
}

func textOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// routerState is what the hub holds of s: running when its connection is
// alive, failed when its last start, list or call got no answer, idle when
// there is none but the tools are known, else unknown. A tools plugin's
// server has no connection (the plugin host serves its tools in this
// process): running when its tools are known, else unknown. When the tools
// were learnt is the backend's last list, else the card's last change.
func routerState(s *Server, c *hubCard) (string, *string, *time.Time) {
	var cached *time.Time
	if c != nil && !c.At.IsZero() {
		cached = &c.At
	}
	if s.Plugin != "" {
		if c != nil {
			return RouterRunning, nil, cached
		}
		return RouterUnknown, nil, nil
	}
	hubPool.Lock()
	b := hubPool.m[s.Name]
	hubPool.Unlock()
	if b != nil && b.key != hubKey(s) {
		b = nil // edited since: the pool ends it on the next call
	}
	if b == nil {
		if c != nil {
			return RouterIdle, nil, cached
		}
		return RouterUnknown, nil, nil
	}
	b.mu.Lock()
	live := b.conn != nil && b.conn.alive()
	why := b.fail
	if why == nil {
		why = b.err
	}
	listed, listedAt := b.listed, b.listedAt
	b.mu.Unlock()
	if listed && !listedAt.IsZero() {
		cached = &listedAt
	}
	switch {
	case why != nil:
		e := routerError(s, why.Error())
		return RouterFailed, &e, cached
	case live:
		return RouterRunning, nil, cached
	case listed || c != nil:
		return RouterIdle, nil, cached
	}
	return RouterUnknown, nil, nil
}

// redactURL is raw as scheme://host[:port]/path: no user, password, query or
// fragment. nil for an address that isn't an http(s) URL.
func redactURL(raw string) *string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil
	}
	s := u.Scheme + "://" + u.Host + u.EscapedPath()
	return &s
}

var textURL = lazyre.New(`https?://[^\s"'<>)]+`)

// routerError is msg, a failure the hub kept, made safe to show: every
// secret of s (a header's or variable's value, an argument, a sign-in app's
// secret, an address's user, password and query) and the home folder are
// taken out, and any address in it is cut to scheme://host/path.
func routerError(s *Server, msg string) string {
	var secrets []string
	add := func(v string) {
		if len(v) >= 4 {
			secrets = append(secrets, v)
		}
	}
	for _, v := range s.Headers {
		add(v)
		if _, tail, ok := strings.Cut(v, " "); ok {
			add(strings.TrimSpace(tail)) // the token of "Bearer <token>"
		}
	}
	for _, v := range s.Env {
		add(v)
	}
	for _, a := range s.Args {
		add(a)
	}
	if s.Command != "" {
		add(s.Command)
		add(filepath.Base(s.Command))
	}
	if s.OAuth != nil {
		add(s.OAuth.ClientSecret)
	}
	if u, err := url.Parse(s.URL); err == nil {
		if u.User != nil {
			add(u.User.Username())
			p, _ := u.User.Password()
			add(p)
		}
		for _, vs := range u.Query() {
			for _, v := range vs {
				add(v)
			}
		}
		add(u.RawQuery)
		add(u.Fragment)
	}
	slices.SortFunc(secrets, func(a, b string) int { return len(b) - len(a) })
	// addresses first, so a secret in one is cut with its address
	msg = textURL.ReplaceAllStringFunc(msg, func(m string) string {
		if r := redactURL(strings.TrimRight(m, ".,;:")); r != nil {
			return *r
		}
		return "…"
	})
	for _, v := range secrets {
		msg = strings.ReplaceAll(msg, v, "…")
	}
	if home, err := os.UserHomeDir(); err == nil && len(home) > 1 {
		msg = strings.ReplaceAll(msg, home, "~")
	}
	return clip(msg, maxRouterError)
}
