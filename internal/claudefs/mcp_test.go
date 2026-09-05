package claudefs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The shape is copied from a real .claude.json on this machine: five stdio
// servers with a command, args, and an env holding database passwords.
const realish = `{
  "numStartups": 41,
  "installMethod": "native",
  "mcpServers": {
    "MSSQL_Main": {
      "command": "node",
      "args": ["C:\\tools\\mssql-mcp\\index.js"],
      "env": {"MSSQL_HOST": "10.0.0.4", "MSSQL_PASSWORD": "hunter2", "MSSQL_USER": "sa"}
    },
    "docs": {"type": "http", "url": "https://example.test/mcp"}
  },
  "projects": {"C:\\work": {"allowedTools": []}}
}`

func writeConfig(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestListMCPServers(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, realish)

	got, err := ListMCPServers(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d servers, want 2: %+v", len(got), got)
	}
	// Sorted, so the order is stable for a UI to render.
	if got[0].Name != "MSSQL_Main" || got[1].Name != "docs" {
		t.Errorf("names = %q, %q", got[0].Name, got[1].Name)
	}
	if got[0].Type != "stdio" {
		t.Errorf("a server with a command is stdio, got %q", got[0].Type)
	}
	if got[1].Type != "http" || got[1].URL == "" {
		t.Errorf("the http server came back as %+v", got[1])
	}
	if !got[0].HasSecrets {
		t.Error("a server with MSSQL_PASSWORD in its env does not say it carries a credential")
	}
}

// The one rule that matters here: a password is never returned, by any route.
func TestListMCPServersNeverReturnsValues(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, realish)

	got, err := ListMCPServers(dir)
	if err != nil {
		t.Fatal(err)
	}
	blob, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), "hunter2") {
		t.Fatalf("the password is in what the API would send: %s", blob)
	}
	// The key is reported, because knowing a server needs one is useful.
	if len(got[0].EnvKeys) != 3 {
		t.Errorf("env keys = %v, want all three names", got[0].EnvKeys)
	}
	for _, k := range got[0].EnvKeys {
		if strings.Contains(k, "hunter2") {
			t.Errorf("a value leaked into the key list: %q", k)
		}
	}
}

// An account that has never been run has no config at all, and none is a real
// answer rather than a failure.
func TestListMCPServersMissingConfig(t *testing.T) {
	got, err := ListMCPServers(t.TempDir())
	if err != nil {
		t.Fatalf("a fresh account should not be an error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %+v, want none", got)
	}
}

func TestCopyMCPServers(t *testing.T) {
	from, to := t.TempDir(), t.TempDir()
	writeConfig(t, from, realish)
	writeConfig(t, to, `{"numStartups": 3, "projects": {"C:\\other": {"x": 1}}}`)

	copied, skipped, err := CopyMCPServers(from, to, []string{"MSSQL_Main", "nope"})
	if err != nil {
		t.Fatal(err)
	}
	if len(copied) != 1 || copied[0] != "MSSQL_Main" {
		t.Errorf("copied = %v", copied)
	}
	if len(skipped) != 1 || skipped[0] != "nope" {
		t.Errorf("skipped = %v", skipped)
	}

	// The value went across, which is the point — this is how the second
	// account can actually reach the database.
	raw, err := os.ReadFile(filepath.Join(to, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "hunter2") {
		t.Error("the credential did not make it, so the copied server cannot connect")
	}
	// And nothing else in the destination was lost by the round trip.
	if !strings.Contains(string(raw), "numStartups") || !strings.Contains(string(raw), "C:\\\\other") {
		t.Errorf("the rest of the config did not survive: %s", raw)
	}

	got, err := ListMCPServers(to)
	if err != nil || len(got) != 1 || got[0].Name != "MSSQL_Main" {
		t.Errorf("destination now lists %+v (%v)", got, err)
	}
}

// A name already configured is far more likely to be a deliberate variant than
// something wanting replacement.
func TestCopyMCPServersDoesNotOverwrite(t *testing.T) {
	from, to := t.TempDir(), t.TempDir()
	writeConfig(t, from, realish)
	writeConfig(t, to, `{"mcpServers": {"MSSQL_Main": {"command": "mine", "env": {"K": "keepme"}}}}`)

	copied, skipped, err := CopyMCPServers(from, to, []string{"MSSQL_Main"})
	if err != nil {
		t.Fatal(err)
	}
	if len(copied) != 0 || len(skipped) != 1 {
		t.Errorf("copied=%v skipped=%v, want it left alone", copied, skipped)
	}
	raw, _ := os.ReadFile(filepath.Join(to, ".claude.json"))
	if !strings.Contains(string(raw), "keepme") {
		t.Error("the existing server was overwritten")
	}
}

// The original is kept, because this is somebody's live configuration.
func TestCopyMCPServersBacksUpFirst(t *testing.T) {
	from, to := t.TempDir(), t.TempDir()
	writeConfig(t, from, realish)
	writeConfig(t, to, `{"numStartups": 3}`)

	if _, _, err := CopyMCPServers(from, to, []string{"MSSQL_Main"}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(to)
	var backups int
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".claude.json.backup-") {
			backups++
		}
	}
	if backups != 1 {
		t.Errorf("found %d backups, want 1: %v", backups, entries)
	}
	// And no temp file left behind.
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("a temp file was left behind: %s", e.Name())
		}
	}
}

func TestCopyMCPServersRefusesTheSameDirectory(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, realish)
	if _, _, err := CopyMCPServers(dir, dir, []string{"MSSQL_Main"}); err == nil {
		t.Error("copying an account onto itself should be refused")
	}
}
