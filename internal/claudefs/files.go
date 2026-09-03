package claudefs

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Per-agent file attribution.
//
// When five agents write to one repository, `git status` is a single blended
// list and ownership is lost. But every edit an agent makes passes through a
// tool call, and every tool call is recorded in that agent's own transcript. So
// the attribution does not have to be inferred or watched for — it can be read
// back out of the transcript that already exists, which is both exact and free.

// FileTouch is one file an agent changed, and how.
type FileTouch struct {
	Path  string    `json:"path"`
	Tool  string    `json:"tool"`
	Edits int       `json:"edits"`
	First time.Time `json:"first"`
	Last  time.Time `json:"last"`
}

// writeTools are the tool names that change a file on disk. Read, Grep and Glob
// are deliberately absent: looking at a file is not touching it, and counting
// reads as edits would attribute half the repository to whichever agent
// explored it.
var writeTools = map[string]bool{
	"Write":        true,
	"Edit":         true,
	"MultiEdit":    true,
	"NotebookEdit": true,
}

// transcriptToolLine is the subset of an assistant record we need to see which
// files were written.
type transcriptToolLine struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Message   struct {
		Content []struct {
			Type  string `json:"type"`
			Name  string `json:"name"`
			Input struct {
				FilePath     string `json:"file_path"`
				NotebookPath string `json:"notebook_path"`
			} `json:"input"`
		} `json:"content"`
	} `json:"message"`
}

// FilesTouched walks a transcript and returns the files that session wrote,
// most recently touched first.
func FilesTouched(path string) ([]FileTouch, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 256*1024), 16*1024*1024)

	byPath := map[string]*FileTouch{}
	for sc.Scan() {
		b := sc.Bytes()
		if len(b) == 0 || b[0] != '{' {
			continue
		}
		var l transcriptToolLine
		if json.Unmarshal(b, &l) != nil || l.Type != "assistant" {
			continue
		}
		ts, _ := time.Parse(time.RFC3339Nano, l.Timestamp)
		for _, c := range l.Message.Content {
			if c.Type != "tool_use" || !writeTools[c.Name] {
				continue
			}
			p := c.Input.FilePath
			if p == "" {
				p = c.Input.NotebookPath
			}
			if p == "" {
				continue
			}
			t, ok := byPath[p]
			if !ok {
				t = &FileTouch{Path: p, Tool: c.Name, First: ts}
				byPath[p] = t
			}
			t.Edits++
			if !ts.IsZero() {
				t.Last = ts
				if t.First.IsZero() {
					t.First = ts
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	out := make([]FileTouch, 0, len(byPath))
	for _, t := range byPath {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Last.Equal(out[j].Last) {
			return out[i].Path < out[j].Path
		}
		return out[i].Last.After(out[j].Last)
	})
	return out, nil
}

// RelativeTouched is FilesTouched with paths made relative to a project root,
// so they line up with what git reports. Files outside the project are dropped:
// an agent that edited something elsewhere is not part of this repository's
// review.
func RelativeTouched(path, projectDir string) ([]FileTouch, error) {
	all, err := FilesTouched(path)
	if err != nil {
		return nil, err
	}
	root, err := filepath.Abs(projectDir)
	if err != nil {
		return all, nil
	}
	var out []FileTouch
	for _, t := range all {
		abs := t.Path
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(root, abs)
		}
		rel, err := filepath.Rel(root, abs)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		t.Path = filepath.ToSlash(rel)
		out = append(out, t)
	}
	return out, nil
}

// Turn is one exchange in a transcript, for the commit-context export.
type Turn struct {
	Role string    `json:"role"`
	Text string    `json:"text"`
	When time.Time `json:"when"`
}

// transcriptTextLine reads the human-readable side of a transcript.
type transcriptTextLine struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Message   struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// Conversation extracts the prose of a session: the prompts and the replies,
// without tool payloads. This is what gets attached to a commit so the next
// person can recover why a change was made, not just what changed.
func Conversation(path string, maxTurns int) ([]Turn, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 256*1024), 16*1024*1024)

	var turns []Turn
	for sc.Scan() {
		b := sc.Bytes()
		if len(b) == 0 || b[0] != '{' {
			continue
		}
		var l transcriptTextLine
		if json.Unmarshal(b, &l) != nil {
			continue
		}
		if l.Type != "user" && l.Type != "assistant" {
			continue
		}
		text := extractText(l.Message.Content)
		if strings.TrimSpace(text) == "" {
			continue
		}
		ts, _ := time.Parse(time.RFC3339Nano, l.Timestamp)
		turns = append(turns, Turn{Role: l.Type, Text: text, When: ts})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if maxTurns > 0 && len(turns) > maxTurns {
		turns = turns[len(turns)-maxTurns:]
	}
	return turns, nil
}

// extractText pulls readable prose out of a content field, which is either a
// bare string or an array of typed blocks.
func extractText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
		return ""
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		// Thinking blocks are internal reasoning and tool blocks are machinery;
		// neither belongs in a record meant for a human reader.
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}
