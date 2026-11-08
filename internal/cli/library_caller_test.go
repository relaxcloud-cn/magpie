package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/library"
)

// magpie library mcp add --caller and magpie library mcp caller <name>
// on|off have the router tell a URL server who is calling it
// (Server.Caller). It is off by default, a command server can't take it,
// and any other word is refused and writes nothing.
func TestLibraryMCPCaller(t *testing.T) {
	h := t.TempDir()
	for k, v := range map[string]string{"HOME": h, "USERPROFILE": h, "XDG_CONFIG_HOME": filepath.Join(h, ".config"), "CLAUDE_CONFIG_DIR": "", "CODEX_HOME": "", "APPDATA": "", "LOCALAPPDATA": ""} {
		t.Setenv(k, v)
	}
	if err := os.MkdirAll(filepath.Join(h, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h, ".claude/settings.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	asked := func(name string) bool {
		t.Helper()
		v, err := library.Read(nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range v.Servers {
			if s.Name == name {
				return s.Caller
			}
		}
		t.Fatalf("no server %s", name)
		return false
	}
	if err := libraryCmd([]string{"library", "mcp", "add", "plain", "https://example.com/mcp", "agents=claude"}); err != nil {
		t.Fatal(err)
	}
	if asked("plain") {
		t.Fatal("a server asks for its caller by default")
	}
	if err := libraryCmd([]string{"library", "mcp", "add", "told", "https://example.com/other", "agents=claude", "--caller"}); err != nil {
		t.Fatal(err)
	}
	if !asked("told") {
		t.Fatal("add --caller left it off")
	}
	if err := libraryCmd([]string{"library", "mcp", "caller", "plain", "on"}); err != nil {
		t.Fatal(err)
	}
	if !asked("plain") {
		t.Fatal("caller on left it off")
	}
	if err := libraryCmd([]string{"library", "mcp", "caller", "told", "off"}); err != nil {
		t.Fatal(err)
	}
	if asked("told") {
		t.Fatal("caller off left it on")
	}
	if err := libraryCmd([]string{"library", "mcp", "caller", "plain", "maybe"}); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("caller plain maybe: %v", err)
	}
	// a command has no request to carry a header
	if err := libraryCmd([]string{"library", "mcp", "add", "cmd", "npx", "-y", "fs", "agents=claude", "--caller"}); err == nil || !strings.Contains(err.Error(), "URL server") {
		t.Fatalf("add --caller for a command: %v", err)
	}
	if err := libraryCmd([]string{"library", "mcp", "add", "cmd", "npx", "-y", "fs", "agents=claude"}); err != nil {
		t.Fatal(err)
	}
	if err := libraryCmd([]string{"library", "mcp", "caller", "cmd", "on"}); err == nil || !strings.Contains(err.Error(), "URL server") {
		t.Fatalf("caller cmd on: %v", err)
	}
	if asked("cmd") {
		t.Fatal("a command server asks for its caller")
	}
}
