package claudefs

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Which files an agent has touched.
//
// It is in the conversation already — every Write and Edit names its file — but
// spread through a transcript you have to scroll, mixed in with everything else
// the agent did. After twenty minutes of work the question "what did it change"
// is answered by reading back through a hundred tool cards.
//
// So the same information, collected: the files, how many times each was
// touched, and when last. Read from the tool calls rather than from git, because
// the two answer different questions — git says what is different from the last
// commit, this says what this agent did. An agent that edited a file and then
// reverted it belongs on this list and not in git's.

// EditedFile is one file an agent wrote to.
type EditedFile struct {
	// Path is as the agent gave it, absolute in practice.
	Path string `json:"path"`
	// Rel is relative to the session's working directory, which is what a person
	// reads. Falls back to Path when the file is outside the project.
	Rel string `json:"rel"`
	// Name is the basename, for when the list is narrow.
	Name string `json:"name"`
	// Edits is how many times it was written to.
	Edits int `json:"edits"`
	// Created is true when the first thing done to it was a Write, which for a
	// file that did not exist is the difference between "new" and "changed".
	Created bool `json:"created,omitempty"`
	// When is the last time it was touched.
	When time.Time `json:"when"`
}

// editTools are the ones that change a file on disk. Read is deliberately not
// here: looking at a file is not touching it, and including it would bury the
// handful of real changes under everything the agent glanced at.
var editTools = map[string]bool{
	"Write": true, "Edit": true, "MultiEdit": true, "NotebookEdit": true,
}

// EditedFiles collects the files written to during a conversation, most recently
// touched first.
func EditedFiles(msgs []Message, cwd string) []EditedFile {
	byPath := map[string]*EditedFile{}
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Kind != BlockTool || b.Tool == nil || !editTools[b.Tool.Name] {
				continue
			}
			p := editPath(b.Tool.Input)
			if p == "" {
				continue
			}
			f, ok := byPath[p]
			if !ok {
				f = &EditedFile{
					Path:    p,
					Rel:     relativeTo(cwd, p),
					Name:    filepath.Base(p),
					Created: b.Tool.Name == "Write",
				}
				byPath[p] = f
			}
			f.Edits++
			if !m.When.IsZero() {
				f.When = m.When
			}
		}
	}
	if len(byPath) == 0 {
		return nil
	}

	out := make([]EditedFile, 0, len(byPath))
	for _, f := range byPath {
		out = append(out, *f)
	}
	// Most recent first: the file being worked on now is the one wanted now.
	sort.Slice(out, func(i, j int) bool {
		if !out[i].When.Equal(out[j].When) {
			return out[i].When.After(out[j].When)
		}
		return out[i].Rel < out[j].Rel
	})
	return out
}

// editPath pulls the file out of a tool's input.
func editPath(input string) string {
	if input == "" {
		return ""
	}
	var in struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	}
	if json.Unmarshal([]byte(input), &in) != nil {
		return ""
	}
	p := in.FilePath
	if p == "" {
		p = in.NotebookPath
	}
	return strings.TrimSpace(p)
}

// relativeTo makes a path readable against the working directory, and leaves it
// alone when it is somewhere else entirely — a path outside the project is
// exactly the case where the full one is worth seeing.
func relativeTo(cwd, p string) string {
	if cwd == "" {
		return p
	}
	rel, err := filepath.Rel(cwd, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		return p
	}
	return filepath.ToSlash(rel)
}
