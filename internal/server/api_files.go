package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/uniair/go-ai-team/internal/gitx"
)

// Reading and editing the files an agent is working on.
//
// The app already showed what agents changed; this lets you look at the code
// itself and fix a line without leaving for an editor. It is not trying to be
// one — there is no project-wide search here and no language server — it is the
// thing you reach for when a review turns up a typo.
//
// Two constraints shape the whole file.
//
// Every path is confined to the project. The only inputs are a project id and a
// relative path, and the resolved file must still be inside that project after
// symlinks are followed. A route that reads and writes arbitrary paths is a
// remote file manager, which is not what anyone installed.
//
// And a write is conditional. This app runs agents that edit these same files,
// so between opening a file and saving it, something else may well have rewritten
// it. Saving blindly would silently destroy an agent's work — so the editor
// carries the hash it loaded, and a save whose hash no longer matches is refused
// rather than applied.

const (
	// maxReadBytes is the largest file the editor will open. Past this it is
	// not something anyone is editing by hand, and sending it would just stall
	// the browser.
	maxReadBytes = 2 << 20
	// maxWriteBytes bounds what can be written back.
	maxWriteBytes = 8 << 20
	// sniffBytes is how much of a file is examined to decide it is binary.
	sniffBytes = 8000
)

// skipDirs are never listed. .git is machinery, and the rest are caches that
// bury a tree in tens of thousands of files nobody wants to scroll past.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, ".venv": true, "venv": true,
	"__pycache__": true, ".next": true, ".nuxt": true, "dist": true,
	"vendor": true, ".idea": true, ".gradle": true, "target": true,
}

// projectPath resolves a project-relative path to a real one inside it.
//
// The check is on the resolved path, after symlinks, because a symlink inside
// the project pointing out of it would otherwise pass a textual test and read
// whatever it aimed at.
func (s *Server) projectPath(projectID, rel string) (string, string, error) {
	p, err := s.st.Project(projectID)
	if err != nil {
		return "", "", err
	}
	return resolveInside(p.Path, rel)
}

// resolveInside joins a relative path onto a root and proves the result is still
// under it. Kept free of the store so the part that matters can be tested on its
// own, which is what security-critical code should be.
func resolveInside(dir, rel string) (string, string, error) {
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", "", fmt.Errorf("the project directory is not reachable: %w", err)
	}

	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == "." || clean == string(filepath.Separator) {
		clean = ""
	}
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("path must be inside the project")
	}
	abs := filepath.Join(root, clean)

	// A path that does not exist yet cannot be resolved, so check its parent.
	probe := abs
	if _, err := os.Lstat(abs); err != nil {
		probe = filepath.Dir(abs)
	}
	real, err := filepath.EvalSymlinks(probe)
	if err != nil {
		return "", "", fmt.Errorf("no such file")
	}
	if real != root && !strings.HasPrefix(real, root+string(filepath.Separator)) {
		return "", "", fmt.Errorf("path must be inside the project")
	}
	return abs, root, nil
}

type fileEntry struct {
	Name string `json:"name"`
	Path string `json:"path"` // project-relative, forward slashes
	Dir  bool   `json:"dir"`
	Size int64  `json:"size,omitempty"`
	// Status is the git short code when the file has changes, so the tree shows
	// what moved without switching to Review.
	Status string `json:"status,omitempty"`
}

// fileTree lists one directory. One level at a time, because a repository can
// hold a hundred thousand files and nobody needs them all to open one.
func (s *Server) fileTree(w http.ResponseWriter, r *http.Request) {
	pid := r.URL.Query().Get("projectId")
	rel := r.URL.Query().Get("path")
	abs, root, err := s.projectPath(pid, rel)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	// Which files git considers changed, so the tree can mark them.
	changed := map[string]string{}
	if gitx.IsRepo(root) {
		if st, err := gitx.GetStatus(r.Context(), root); err == nil {
			for _, f := range st.Files {
				changed[filepath.ToSlash(f.Path)] = strings.TrimSpace(f.Code)
			}
		}
	}

	out := []fileEntry{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() && skipDirs[name] {
			continue
		}
		relPath := filepath.ToSlash(filepath.Join(filepath.FromSlash(rel), name))
		relPath = strings.TrimPrefix(relPath, "./")
		fe := fileEntry{Name: name, Path: relPath, Dir: e.IsDir()}
		if !e.IsDir() {
			if info, err := e.Info(); err == nil {
				fe.Size = info.Size()
			}
			fe.Status = changed[relPath]
		}
		out = append(out, fe)
	}
	// Directories first, then by name. Anything else makes a tree hard to scan.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"path":    filepath.ToSlash(rel),
		"entries": out,
	})
}

type fileBody struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Size      int64  `json:"size"`
	SHA       string `json:"sha"`
	Binary    bool   `json:"binary"`
	Truncated bool   `json:"truncated"`
	// ReadOnly is set for files that can be shown but should not be edited here.
	ReadOnly bool   `json:"readOnly"`
	Reason   string `json:"reason,omitempty"`
}

// readFile returns a file's contents plus the hash a later save must match.
func (s *Server) readFile(w http.ResponseWriter, r *http.Request) {
	abs, _, err := s.projectPath(r.URL.Query().Get("projectId"), r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	st, err := os.Stat(abs)
	if err != nil || st.IsDir() {
		writeErr(w, http.StatusNotFound, fmt.Errorf("no such file"))
		return
	}

	out := fileBody{Path: filepath.ToSlash(r.URL.Query().Get("path")), Size: st.Size()}
	if st.Size() > maxReadBytes {
		out.ReadOnly, out.Truncated = true, true
		out.Reason = fmt.Sprintf("this file is %s; the editor opens files up to %d MB",
			human(st.Size()), maxReadBytes>>20)
		writeJSON(w, http.StatusOK, out)
		return
	}

	b, err := os.ReadFile(abs)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if isBinary(b) {
		out.Binary, out.ReadOnly = true, true
		out.Reason = "this looks like a binary file, so it is not shown as text"
		out.SHA = hashOf(b)
		writeJSON(w, http.StatusOK, out)
		return
	}
	out.Content = string(b)
	out.SHA = hashOf(b)
	writeJSON(w, http.StatusOK, out)
}

type writeReq struct {
	ProjectID string `json:"projectId"`
	Path      string `json:"path"`
	Content   string `json:"content"`
	// SHA is the hash of the contents this edit started from. A save is refused
	// if the file no longer matches, because something else has written it.
	SHA string `json:"sha"`
}

// writeFile saves an edit, but only onto the version it was made against.
func (s *Server) writeFile(w http.ResponseWriter, r *http.Request) {
	var req writeReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if len(req.Content) > maxWriteBytes {
		writeErr(w, http.StatusRequestEntityTooLarge,
			fmt.Errorf("that is larger than the %d MB this editor will write", maxWriteBytes>>20))
		return
	}
	abs, _, err := s.projectPath(req.ProjectID, req.Path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	cur, err := os.ReadFile(abs)
	if err != nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("no such file"))
		return
	}
	// The conflict check. An agent may have rewritten this file since it was
	// opened, and overwriting that silently is the one unrecoverable thing this
	// endpoint could do.
	if req.SHA != "" && hashOf(cur) != req.SHA {
		writeErr(w, http.StatusConflict, fmt.Errorf(
			"this file changed on disk since you opened it — most likely an agent edited it. "+
				"Reload it to see the new version; your text is still in the editor"))
		return
	}

	st, err := os.Stat(abs)
	mode := os.FileMode(0o644)
	if err == nil {
		mode = st.Mode().Perm()
	}
	// Written via a temporary file in the same directory and renamed, so a
	// failure halfway cannot leave a half-written source file behind.
	tmp := abs + ".goaiteam.tmp"
	if err := os.WriteFile(tmp, []byte(req.Content), mode); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if err := os.Rename(tmp, abs); err != nil {
		_ = os.Remove(tmp)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path": filepath.ToSlash(req.Path),
		"sha":  hashOf([]byte(req.Content)),
		"size": len(req.Content),
	})
}

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:12])
}

// isBinary decides whether to show a file as text.
//
// A NUL byte is the giveaway: no text encoding this editor would display puts
// one in the middle of a file, and every binary format has them early.
func isBinary(b []byte) bool {
	if len(b) > sniffBytes {
		b = b[:sniffBytes]
	}
	for _, c := range b {
		if c == 0 {
			return true
		}
	}
	return false
}

func human(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
