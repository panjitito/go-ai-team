package server

import (
	"context"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/uniair/go-ai-team/internal/gitx"
)

// Finding a file by typing a few letters of its name.
//
// Claude Code completes file paths when you type "@". The composer here had no
// equivalent, so referring to a file meant knowing its path exactly and typing
// all of it — and a path with a typo in it is worse than no path, because the
// agent goes looking and reports that it does not exist.
//
// The file list comes from git where there is a repository: `git ls-files` plus
// the untracked-but-not-ignored ones is both faster than walking and, more
// importantly, already excludes node_modules, build output and everything else
// .gitignore covers. Somewhere without a repository falls back to a bounded
// walk, which is the same list a person would expect and no more expensive than
// it has to be.

// findResult is one candidate path.
type findResult struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Dir  bool   `json:"dir"`
}

func (s *Server) findFiles(w http.ResponseWriter, r *http.Request) {
	pid := r.URL.Query().Get("projectId")
	_, root, err := s.projectPath(pid, "")
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := 25
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, e := strconv.Atoi(v); e == nil && n > 0 && n <= 200 {
			limit = n
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), findBudget)
	defer cancel()

	paths := listProjectFiles(ctx, root)
	writeJSON(w, http.StatusOK, rankPaths(paths, q, limit))
}

// findBudget bounds the listing. A cold, very large repository can take a
// moment, and the composer must never feel like it has stopped responding.
const findBudget = 3 * time.Second

// walkMax stops the fallback walk from reading a whole disk if a project is
// pointed somewhere unexpected.
const walkMax = 40000

// listProjectFiles returns the project's files as slash-separated relative
// paths.
func listProjectFiles(ctx context.Context, root string) []string {
	if gitx.IsRepo(root) {
		if out, err := gitx.ListFiles(ctx, root); err == nil && len(out) > 0 {
			return out
		}
	}
	var paths []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil || len(paths) >= walkMax {
			return filepath.SkipAll
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	return paths
}

// rankPaths orders candidates the way a person means them.
//
// The name you are typing is nearly always the file's own, not a directory half
// way up its path — so a match on the basename beats a match anywhere, and a
// prefix of the basename beats a match in the middle of it. Shorter paths win
// ties, because the top-level README is much more often the one meant than the
// one four directories down.
func rankPaths(paths []string, q string, limit int) []findResult {
	out := make([]findResult, 0, limit)
	if q == "" {
		// No query yet: the shortest paths, which is a reasonable "what is in
		// this project" answer and puts the top level first.
		sorted := append([]string(nil), paths...)
		sort.Slice(sorted, func(i, j int) bool {
			if len(sorted[i]) != len(sorted[j]) {
				return len(sorted[i]) < len(sorted[j])
			}
			return sorted[i] < sorted[j]
		})
		for _, p := range sorted {
			if len(out) >= limit {
				break
			}
			out = append(out, findResult{Path: p, Name: path.Base(p)})
		}
		return out
	}

	lq := strings.ToLower(q)
	type scored struct {
		p     string
		score int
	}
	var hits []scored
	for _, p := range paths {
		lp := strings.ToLower(p)
		base := path.Base(lp)
		switch {
		case strings.HasPrefix(base, lq):
			hits = append(hits, scored{p, 0})
		case strings.Contains(base, lq):
			hits = append(hits, scored{p, 1})
		case strings.Contains(lp, lq):
			hits = append(hits, scored{p, 2})
		case subsequence(lq, lp):
			// "isrv" finds internal/server. Last, because it matches loosely.
			hits = append(hits, scored{p, 3})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score < hits[j].score
		}
		if len(hits[i].p) != len(hits[j].p) {
			return len(hits[i].p) < len(hits[j].p)
		}
		return hits[i].p < hits[j].p
	})
	for _, h := range hits {
		if len(out) >= limit {
			break
		}
		out = append(out, findResult{Path: h.p, Name: path.Base(h.p)})
	}
	return out
}

func subsequence(short, long string) bool {
	i := 0
	for j := 0; i < len(short) && j < len(long); j++ {
		if short[i] == long[j] {
			i++
		}
	}
	return i == len(short)
}
