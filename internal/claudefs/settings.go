package claudefs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The one setting this app writes.
//
// Everything else here reads. A config directory belongs to the person who
// signed in with it, and Go AI Team's whole design is to choose which one a
// process starts in rather than to edit what is inside. The status line is the
// exception, and only when asked: the five-hour and weekly figures exist
// nowhere but the payload the CLI hands a status-line command, so on an account
// with none there is nothing to read and no way to get it without adding one.
//
// It is written back in a shape that can be taken out again, it never replaces
// a command somebody else put there without being told to, and the file it
// edits is rewritten whole from what it parsed, so an unknown setting survives.

// StatusLineSetting is what settings.json holds under "statusLine".
type StatusLineSetting struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Padding *int   `json:"padding,omitempty"`
}

// settingsPath is the file a config directory keeps its settings in.
func settingsPath(dir string) string { return filepath.Join(dir, "settings.json") }

// readSettings loads a config directory's settings as a plain map, so writing
// one key back cannot drop the rest.
func readSettings(dir string) (map[string]json.RawMessage, error) {
	b, err := os.ReadFile(settingsPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON, so it is not safe to edit: %w", settingsPath(dir), err)
	}
	return m, nil
}

// StatusLineOf reports the status-line command configured for a config
// directory, and whether there is one at all.
func StatusLineOf(dir string) (StatusLineSetting, bool) {
	if dir == "" {
		return StatusLineSetting{}, false
	}
	m, err := readSettings(dir)
	if err != nil {
		return StatusLineSetting{}, false
	}
	raw, ok := m["statusLine"]
	if !ok {
		return StatusLineSetting{}, false
	}
	var sl StatusLineSetting
	if err := json.Unmarshal(raw, &sl); err != nil {
		return StatusLineSetting{}, false
	}
	if strings.TrimSpace(sl.Command) == "" {
		return StatusLineSetting{}, false
	}
	return sl, true
}

// ownStatusLineMark is what makes one of ours recognisable later. The command
// is an absolute path that changes when the binary moves, so the subcommand
// name is the stable part to look for.
const ownStatusLineMark = " statusline"

// IsOwnStatusLine reports whether a configured command is this app's.
//
// Checked before removing one, so taking ours out never takes out somebody
// else's.
func IsOwnStatusLine(sl StatusLineSetting) bool {
	c := strings.TrimSpace(sl.Command)
	return strings.HasSuffix(c, ownStatusLineMark) ||
		strings.HasSuffix(strings.ToLower(c), ownStatusLineMark)
}

// InstallStatusLine points a config directory's status line at this binary.
//
// exe is the path to run, quoted here rather than by the caller because it
// usually contains a space on Windows. An existing command from somewhere else
// is left alone unless replace is set, since overwriting somebody's own status
// line without asking is exactly the kind of edit this package does not make.
func InstallStatusLine(dir, exe string, replace bool) error {
	if dir == "" {
		return errors.New("no config directory")
	}
	if strings.TrimSpace(exe) == "" {
		return errors.New("no path to this program, so there is no command to install")
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return fmt.Errorf("%s is not a directory that exists", dir)
	}
	if cur, ok := StatusLineOf(dir); ok && !IsOwnStatusLine(cur) && !replace {
		return fmt.Errorf("that account already has its own status line (%s); replacing it is a separate decision", cur.Command)
	}

	m, err := readSettings(dir)
	if err != nil {
		return err
	}
	sl, err := json.Marshal(StatusLineSetting{Type: "command", Command: quote(exe) + ownStatusLineMark})
	if err != nil {
		return err
	}
	m["statusLine"] = sl
	return writeSettings(dir, m)
}

// RemoveStatusLine takes ours back out, and refuses to touch one that is not.
func RemoveStatusLine(dir string) error {
	cur, ok := StatusLineOf(dir)
	if !ok {
		return nil
	}
	if !IsOwnStatusLine(cur) {
		return fmt.Errorf("that status line is not one this app installed (%s), so it is not this app's to remove", cur.Command)
	}
	m, err := readSettings(dir)
	if err != nil {
		return err
	}
	delete(m, "statusLine")
	return writeSettings(dir, m)
}

// writeSettings rewrites the file from the map, indented the way the CLI writes
// it, and replaces the old one only once the new one is safely on disk.
func writeSettings(dir string, m map[string]json.RawMessage) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')

	path := settingsPath(dir)
	tmp := path + ".goaiteam-tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// quote wraps a path for a shell command line if it needs it.
func quote(p string) string {
	if strings.HasPrefix(p, `"`) {
		return p
	}
	if strings.ContainsAny(p, " \t") {
		return `"` + p + `"`
	}
	return p
}
