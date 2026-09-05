package claudefs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The MCP servers an account can reach.
//
// They live in <configdir>/.claude.json under "mcpServers", and that file is not
// part of the shared user layer — it cannot be, because it also holds the
// session history and per-project state that make accounts separate in the first
// place. The consequence catches people out: sign a second account in, turn
// sharing on, and its commands, skills and agents all appear, while every MCP
// server you had is silently absent. Nothing says so; the tools are just not
// there, and the agent explains it cannot reach your database.
//
// So the servers are listed per account, and copied between them on request.
//
// Credentials never come back out. A server's env commonly holds a password or
// a token — the ones on this machine hold database passwords — so the listing
// reports which keys exist and never what is in them. Copying moves the values
// from one file to the other without them passing through the API, a log, or a
// command line where every other process could read them.

// MCPServer is one server as the UI needs to see it.
type MCPServer struct {
	Name string `json:"name"`
	// Type is stdio, http, sse or ws. Absent in the file means stdio.
	Type    string   `json:"type"`
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	URL     string   `json:"url,omitempty"`
	// EnvKeys are the names of the environment variables the server is given.
	// The values are deliberately not here: several of them are passwords.
	EnvKeys []string `json:"envKeys,omitempty"`
	// HasSecrets is true when any of those look like a credential, so the UI can
	// say what is about to be copied.
	HasSecrets bool `json:"hasSecrets,omitempty"`
}

// mcpConfigPath is where an account's servers are configured.
func mcpConfigPath(dir string) string { return filepath.Join(dir, ".claude.json") }

// ListMCPServers reads one account's servers.
//
// A missing file is not an error: an account that has never run has no config
// yet, and "none" is the right answer rather than a failure.
func ListMCPServers(dir string) ([]MCPServer, error) {
	raw, err := readMCPConfig(dir)
	if err != nil {
		return nil, err
	}
	servers := rawServers(raw)
	out := make([]MCPServer, 0, len(servers))
	for name, def := range servers {
		out = append(out, describeServer(name, def))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func readMCPConfig(dir string) (map[string]json.RawMessage, error) {
	b, err := os.ReadFile(mcpConfigPath(dir))
	if os.IsNotExist(err) {
		return map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return nil, err
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("%s is not readable as JSON: %w", mcpConfigPath(dir), err)
	}
	return doc, nil
}

func rawServers(doc map[string]json.RawMessage) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	if v, ok := doc["mcpServers"]; ok {
		_ = json.Unmarshal(v, &out)
	}
	return out
}

func describeServer(name string, def json.RawMessage) MCPServer {
	var d struct {
		Type    string            `json:"type"`
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		URL     string            `json:"url"`
		Env     map[string]string `json:"env"`
	}
	_ = json.Unmarshal(def, &d)

	s := MCPServer{Name: name, Type: d.Type, Command: d.Command, Args: d.Args, URL: d.URL}
	if s.Type == "" {
		if d.URL != "" {
			s.Type = "http"
		} else {
			s.Type = "stdio"
		}
	}
	for k := range d.Env {
		s.EnvKeys = append(s.EnvKeys, k)
		if looksSecret(k) {
			s.HasSecrets = true
		}
	}
	sort.Strings(s.EnvKeys)
	return s
}

// looksSecret is a naming heuristic, used only to warn. It decides whether the
// UI says "this carries a credential", never whether a value is protected —
// values are never returned either way.
func looksSecret(key string) bool {
	k := strings.ToLower(key)
	for _, hint := range []string{"password", "passwd", "secret", "token", "key", "pwd", "auth", "credential"} {
		if strings.Contains(k, hint) {
			return true
		}
	}
	return false
}

// CopyMCPServers copies named servers from one account's config into another's,
// and reports which ones were written.
//
// Values and all: this is a file-to-file copy, so a database password goes
// across without being read into the API, written to a log, or passed on a
// command line where anything else on the machine could see it.
//
// Existing servers of the same name are left alone rather than overwritten. A
// name that is already configured is far more likely to be a deliberate variant
// than something wanting replacement, and overwriting one silently would lose a
// working setup.
func CopyMCPServers(fromDir, toDir string, names []string) (copied []string, skipped []string, err error) {
	if sameFile(mcpConfigPath(fromDir), mcpConfigPath(toDir)) {
		return nil, nil, fmt.Errorf("that is the same configuration directory")
	}
	src, err := readMCPConfig(fromDir)
	if err != nil {
		return nil, nil, err
	}
	srcServers := rawServers(src)

	dst, err := readMCPConfig(toDir)
	if err != nil {
		return nil, nil, err
	}
	dstServers := rawServers(dst)

	for _, n := range names {
		def, ok := srcServers[n]
		if !ok {
			skipped = append(skipped, n)
			continue
		}
		if _, exists := dstServers[n]; exists {
			skipped = append(skipped, n)
			continue
		}
		dstServers[n] = def
		copied = append(copied, n)
	}
	if len(copied) == 0 {
		return nil, skipped, nil
	}

	merged, err := json.Marshal(dstServers)
	if err != nil {
		return nil, nil, err
	}
	dst["mcpServers"] = merged
	if err := writeMCPConfig(toDir, dst); err != nil {
		return nil, nil, err
	}
	return copied, skipped, nil
}

// writeMCPConfig rewrites the account's config with everything else in it
// untouched.
//
// The file belongs to the CLI and holds far more than MCP servers — history,
// per-project state, onboarding flags. It is read back as raw JSON and written
// back the same way so that nothing this code does not understand is dropped by
// a round trip through a struct.
//
// A copy of the original is kept beside it first. This is somebody's live
// configuration, and an atomic replace still replaces.
func writeMCPConfig(dir string, doc map[string]json.RawMessage) error {
	path := mcpConfigPath(dir)
	if b, err := os.ReadFile(path); err == nil {
		backup := fmt.Sprintf("%s.backup-%s", path, time.Now().Format("20060102-150405"))
		if err := os.WriteFile(backup, b, 0o600); err != nil {
			return fmt.Errorf("could not back up %s first: %w", path, err)
		}
	}

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return err
	}
	// Rename over the original, so a reader sees one file or the other and never
	// a half-written one.
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func sameFile(a, b string) bool {
	ra, err1 := filepath.Abs(a)
	rb, err2 := filepath.Abs(b)
	if err1 != nil || err2 != nil {
		return a == b
	}
	return strings.EqualFold(ra, rb)
}
