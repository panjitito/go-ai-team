package claudefs

import (
	"path/filepath"
	"testing"
	"time"
)

func edit(name, input string, when time.Time) Message {
	return Message{Role: "assistant", When: when, Blocks: []Block{
		{Kind: BlockTool, Tool: &ToolCall{Name: name, Input: input}},
	}}
}

func TestEditedFiles(t *testing.T) {
	cwd := filepath.FromSlash("C:/proj")
	t0 := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	msgs := []Message{
		edit("Write", `{"file_path":"C:/proj/internal/a.go"}`, t0),
		edit("Read", `{"file_path":"C:/proj/internal/never-touched.go"}`, t0.Add(time.Minute)),
		edit("Edit", `{"file_path":"C:/proj/internal/a.go"}`, t0.Add(2*time.Minute)),
		edit("Edit", `{"file_path":"C:/proj/README.md"}`, t0.Add(3*time.Minute)),
		edit("Bash", `{"command":"ls"}`, t0.Add(4*time.Minute)),
	}

	got := EditedFiles(msgs, cwd)
	if len(got) != 2 {
		t.Fatalf("got %d files, want 2: %+v", len(got), got)
	}
	// Most recent first: the file being worked on now is the one wanted now.
	if got[0].Rel != "README.md" {
		t.Errorf("first = %q, want the most recently touched", got[0].Rel)
	}
	if got[1].Rel != "internal/a.go" || got[1].Edits != 2 {
		t.Errorf("second = %+v, want internal/a.go touched twice", got[1])
	}
	// Written before it was edited, which is how "new" is told from "changed".
	if !got[1].Created {
		t.Error("a file whose first tool was Write is not marked as created")
	}
	if got[0].Name != "README.md" {
		t.Errorf("name = %q", got[0].Name)
	}
}

// Reading a file is not touching it. Including Read would bury the handful of
// real changes under everything the agent glanced at.
func TestEditedFilesIgnoresReads(t *testing.T) {
	msgs := []Message{
		edit("Read", `{"file_path":"C:/proj/a.go"}`, time.Now()),
		edit("Grep", `{"pattern":"x","path":"C:/proj"}`, time.Now()),
	}
	if got := EditedFiles(msgs, "C:/proj"); got != nil {
		t.Errorf("got %+v, want nothing", got)
	}
}

// A path outside the project is exactly the case where the full one is worth
// seeing, so it is not mangled into a pile of "..".
func TestEditedFilesOutsideTheProject(t *testing.T) {
	cwd := filepath.FromSlash("C:/proj")
	elsewhere := filepath.FromSlash("D:/other/thing.txt")
	got := EditedFiles([]Message{
		edit("Write", `{"file_path":"D:/other/thing.txt"}`, time.Now()),
	}, cwd)
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Rel != elsewhere && got[0].Rel != "D:/other/thing.txt" {
		t.Errorf("rel = %q, want the full path for a file outside the project", got[0].Rel)
	}
}

func TestEditedFilesNotebook(t *testing.T) {
	got := EditedFiles([]Message{
		edit("NotebookEdit", `{"notebook_path":"C:/proj/run.ipynb"}`, time.Now()),
	}, "C:/proj")
	if len(got) != 1 || got[0].Name != "run.ipynb" {
		t.Errorf("got %+v, want the notebook", got)
	}
}

func TestEditedFilesIgnoresRubbish(t *testing.T) {
	for _, in := range []string{"", "not json", `{}`, `{"file_path":"  "}`} {
		if got := EditedFiles([]Message{edit("Write", in, time.Now())}, "C:/proj"); got != nil {
			t.Errorf("input %q produced %+v", in, got)
		}
	}
}
