package server

import "testing"

// Paths from this repository, which is the shape the ranking is for: a few
// hundred files, names repeated across directories, and tests sitting next to
// what they test.
var findCorpus = []string{
	"README.md",
	"main.go",
	"go.mod",
	"internal/desktop/README.md",
	"internal/server/server.go",
	"internal/server/api_files.go",
	"internal/server/web/ask.js",
	"internal/server/web/runbar.js",
	"internal/server/uirunbar_test.go",
	"internal/session/statusline.go",
	"internal/session/statusline_test.go",
	"internal/session/ask.go",
}

func paths(rs []findResult) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Path
	}
	return out
}

// The name you are typing is the file's own, not a directory half way up its
// path, so a basename match has to come first.
func TestRankPathsPrefersTheBasename(t *testing.T) {
	got := paths(rankPaths(findCorpus, "server", 5))
	if len(got) == 0 || got[0] != "internal/server/server.go" {
		t.Errorf("got %v, want server.go first", got)
	}
}

// Two files share a name; the shorter path is far more often the one meant.
func TestRankPathsPrefersTheShorterPath(t *testing.T) {
	got := paths(rankPaths(findCorpus, "readme", 5))
	if len(got) != 2 || got[0] != "README.md" {
		t.Errorf("got %v, want the top-level README first", got)
	}
}

// Case does not matter: nobody types the capitals.
func TestRankPathsIsCaseInsensitive(t *testing.T) {
	if got := paths(rankPaths(findCorpus, "MAIN.GO", 5)); len(got) == 0 || got[0] != "main.go" {
		t.Errorf("got %v, want main.go", got)
	}
}

// A loose match earns its place — "isrv" for internal/server — but must never
// outrank a real one.
func TestRankPathsSubsequenceIsLast(t *testing.T) {
	got := paths(rankPaths(findCorpus, "askjs", 5))
	if len(got) == 0 || got[0] != "internal/server/web/ask.js" {
		t.Errorf("got %v, want ask.js", got)
	}

	got = paths(rankPaths(findCorpus, "statusline", 5))
	if len(got) < 2 || got[0] != "internal/session/statusline.go" {
		t.Errorf("got %v, want statusline.go before its test", got)
	}
}

func TestRankPathsEmptyQueryShowsTheTopLevel(t *testing.T) {
	got := paths(rankPaths(findCorpus, "", 3))
	for _, p := range got {
		if len(p) > len("internal/") {
			t.Errorf("got %v — an empty query should surface the shallowest paths", got)
			break
		}
	}
}

func TestRankPathsRespectsTheLimit(t *testing.T) {
	if got := rankPaths(findCorpus, "e", 3); len(got) != 3 {
		t.Errorf("got %d results, want 3", len(got))
	}
	// A list endpoint answers with an empty list, never null — see lists.go.
	if got := rankPaths(findCorpus, "zzzzz", 5); got == nil || len(got) != 0 {
		t.Errorf("got %#v, want an empty list", got)
	}
}
