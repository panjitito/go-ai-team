package claudefs

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

/* Finding the thing an agent said three days ago.

   Everything an agent has ever done is on this disk already — the transcripts
   are the record — and until now the only way back into any of it was to
   remember which conversation it was and still have that session running. On
   the machine this was written for that is 2.4 GB across 1,127 transcripts, so
   "I know one of them worked this out, I just don't know which" was a question
   with no answer.

   The scan is a raw substring match over the bytes before anything is parsed,
   which is what makes it quick enough to be worth having: 264 MB — the largest
   single project here — takes under a second, because JSON parsing only happens
   for the handful of lines that already matched. A line that matches the raw
   bytes but has the query only in its plumbing (a uuid, an escaped path inside
   a field nobody reads) is dropped after parsing rather than reported, so the
   prefilter can stay cheap without the results getting sloppy. */

// Hit is one message that matched.
type Hit struct {
	// SessionID is the CLI's own conversation id, taken from the file name.
	SessionID string `json:"sessionId"`
	// Dir is the account directory the conversation lives in, and Project is the
	// working directory it ran in.
	Dir     string `json:"dir"`
	Project string `json:"project"`
	// Sub names the subagent whose own transcript this came from, empty when the
	// hit is in the conversation itself.
	Sub  string    `json:"sub,omitempty"`
	When time.Time `json:"when"`
	Role string    `json:"role"`
	// Snippet is the matching text with a little either side; Text is the whole
	// message, capped, for when the snippet is not enough.
	Snippet string `json:"snippet"`
	Text    string `json:"text"`
}

// SearchOpts says what to search and how long to spend on it.
type SearchOpts struct {
	// Dirs are account configuration directories.
	Dirs []string
	// CWD limits the search to conversations that ran in one directory. Empty
	// searches every project in every account directory.
	CWD   string
	Query string
	// Limit caps the hits returned; Budget caps the bytes read and Deadline the
	// time spent, whichever comes first.
	Limit    int
	Budget   int64
	Deadline time.Duration
}

// SearchResult is what was found and how much of the haystack was looked at.
type SearchResult struct {
	Hits []Hit `json:"hits"`
	// Files and Bytes are what was actually read, and Truncated says the search
	// ran out of budget before it ran out of transcripts — so "no hits" means
	// "not in the part I read", which is a different answer.
	Files     int   `json:"files"`
	Bytes     int64 `json:"bytes"`
	Total     int   `json:"total"`
	Truncated bool  `json:"truncated"`
}

const (
	defaultSearchLimit = 120
	// A budget high enough that the deadline is what actually stops a search on
	// this corpus, and low enough to be a guard on one ten times the size.
	defaultSearchBudget = 4 << 30
	defaultSearchTime   = 6 * time.Second

	// searchWorkers is how many transcripts are read at once. See the comment in
	// Search: four, because four was measured to be the fastest here and six was
	// slower.
	searchWorkers = 4

	// searchLineCap skips a single line longer than this.
	//
	// A tool result can be a whole file, and a 30 MB line is not a message
	// anybody is searching for — it is a paste. Reading it costs more than
	// every real message in the conversation put together.
	searchLineCap = 2 << 20

	snippetPad = 90
	textCap    = 4000
)

// Search looks for a string in the transcripts, newest conversation first.
func Search(o SearchOpts) SearchResult {
	q := strings.ToLower(strings.TrimSpace(o.Query))
	res := SearchResult{Hits: []Hit{}}
	if q == "" {
		return res
	}
	if o.Limit <= 0 {
		o.Limit = defaultSearchLimit
	}
	if o.Budget <= 0 {
		o.Budget = defaultSearchBudget
	}
	if o.Deadline <= 0 {
		o.Deadline = defaultSearchTime
	}

	files := searchFiles(o.Dirs, o.CWD)
	res.Total = len(files)
	needle := []byte(q)
	stop := time.Now().Add(o.Deadline)

	// Four readers, measured rather than guessed.
	//
	// One thread manages 323 MB/s on this machine and four manage 1,094 — the
	// work is a read and a pass over the bytes, and neither the disk nor one core
	// is busy while the other waits. Six was slower than four, so this does not
	// scale with the core count and is not written as if it does. It is the
	// difference between a whole-machine search finishing and timing out.
	workers := runtime.NumCPU()
	if workers > searchWorkers {
		workers = searchWorkers
	}
	if workers < 1 {
		workers = 1
	}

	var (
		mu        sync.Mutex
		bytesRead atomic.Int64
		filesRead atomic.Int64
		hitCount  atomic.Int64
		truncated atomic.Bool
		wg        sync.WaitGroup
	)
	work := make(chan transcript)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range work {
				// Checked per file rather than per line: a file is the unit that
				// can be skipped without leaving half a conversation searched.
				if bytesRead.Load() >= o.Budget || hitCount.Load() >= int64(o.Limit) ||
					time.Now().After(stop) {
					truncated.Store(true)
					continue // drain, so the feeder is never left blocked
				}
				filesRead.Add(1)
				hits, read := scanTranscript(f, needle, o.Limit)
				bytesRead.Add(read)
				if len(hits) == 0 {
					continue
				}
				hitCount.Add(int64(len(hits)))
				mu.Lock()
				res.Hits = append(res.Hits, hits...)
				mu.Unlock()
			}
		}()
	}
	// Newest first into the queue, so a search that runs out of budget has spent
	// it on the conversations most likely to be the one wanted.
	for _, f := range files {
		work <- f
	}
	close(work)
	wg.Wait()

	res.Files = int(filesRead.Load())
	res.Bytes = bytesRead.Load()
	res.Truncated = truncated.Load()

	// Newest first across conversations, which is not the order they were read
	// in once several workers have contributed.
	sort.SliceStable(res.Hits, func(i, j int) bool { return res.Hits[i].When.After(res.Hits[j].When) })
	if len(res.Hits) > o.Limit {
		res.Hits = res.Hits[:o.Limit]
		res.Truncated = true
	}
	return res
}

type transcript struct {
	path    string
	dir     string
	project string
	session string
	// sub is the name of a subagent's own transcript, empty for the
	// conversation itself.
	sub string
	mod time.Time
}

// searchFiles lists the transcripts to look at, newest first.
//
// Recursively, because most of them are not where you would first look. A
// conversation is `<project>/<id>.jsonl`, but beside it sits `<project>/<id>/`
// holding `subagents/…/agent-*.jsonl` — one per subagent the conversation
// spawned — and on this machine that is 858 files of the 1,127 there are. The
// main transcript records only that a subagent ran, so searching the top level
// alone would miss most of the work and all of the detail.
func searchFiles(dirs []string, cwd string) []transcript {
	var out []transcript
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		roots := []string{}
		if cwd != "" {
			roots = append(roots, filepath.Join(dir, "projects", EncodeCWD(cwd)))
		} else {
			entries, err := os.ReadDir(filepath.Join(dir, "projects"))
			if err != nil {
				continue
			}
			for _, e := range entries {
				if e.IsDir() {
					roots = append(roots, filepath.Join(dir, "projects", e.Name()))
				}
			}
		}
		for _, root := range roots {
			out = append(out, walkTranscripts(root, dir)...)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].mod.After(out[j].mod) })
	return out
}

func walkTranscripts(root, dir string) []transcript {
	var out []transcript
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(p) != ".jsonl" {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		// The conversation this belongs to: its own name at the top level, and
		// the directory it lives under anywhere below that — which is the
		// parent conversation's id, so a hit in a subagent still leads back to
		// the session that ran it.
		session := strings.TrimSuffix(filepath.Base(p), ".jsonl")
		sub := ""
		if parts := strings.Split(filepath.ToSlash(rel), "/"); len(parts) > 1 {
			session = parts[0]
			sub = strings.TrimSuffix(filepath.Base(p), ".jsonl")
		}
		out = append(out, transcript{
			path: p, dir: dir, project: filepath.Base(root),
			session: session, sub: sub, mod: fi.ModTime(),
		})
		return nil
	})
	return out
}

// scanTranscript reads one file. It returns what it found and how much it read,
// so the caller can hold a budget across files and workers.
func scanTranscript(f transcript, needle []byte, limit int) ([]Hit, int64) {
	fh, err := os.Open(f.path)
	if err != nil {
		return nil, 0
	}
	defer fh.Close()

	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 0, 256<<10), searchLineCap)

	var read int64
	var hits []Hit
	lower := make([]byte, 0, 64<<10)

	for sc.Scan() {
		line := sc.Bytes()
		read += int64(len(line))
		if len(line) == 0 {
			continue
		}
		// Lowercase into a buffer that is reused, so a million-line transcript
		// is not a million allocations.
		lower = lower[:0]
		lower = append(lower, line...)
		asciiLower(lower)
		if !bytes.Contains(lower, needle) {
			continue
		}
		if h, ok := hitFrom(line, needle, f); ok {
			hits = append(hits, h)
			if len(hits) >= limit {
				return hits, read
			}
		}
	}
	// A line over the cap makes the scanner stop; the rest of that conversation
	// is simply not searched, which is better than refusing to search any of it.
	return hits, read
}

// asciiLower lowercases in place. The transcripts are JSON, so the interesting
// text is overwhelmingly ASCII, and a full Unicode fold would cost a re-encode
// of every line to catch the handful of cases where it differs.
func asciiLower(b []byte) {
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
}

// hitFrom turns a matching line into a hit, or says the match was plumbing.
func hitFrom(line, needle []byte, f transcript) (Hit, bool) {
	var cl convLine
	if err := json.Unmarshal(line, &cl); err != nil {
		return Hit{}, false
	}
	role := cl.Message.Role
	if role == "" {
		role = cl.Type
	}

	for _, part := range searchableParts(cl) {
		low := strings.ToLower(part)
		at := strings.Index(low, string(needle))
		if at < 0 {
			continue
		}
		when, _ := time.Parse(time.RFC3339, cl.Timestamp)
		return Hit{
			SessionID: f.session,
			Dir:       f.dir,
			Project:   f.project,
			Sub:       f.sub,
			When:      when,
			Role:      role,
			Snippet:   snippetAround(part, at, len(needle)),
			Text:      cap4k(part),
		}, true
	}
	return Hit{}, false
}

// searchableParts is the text of a message, as a person would read it.
//
// Tool calls and their results are included: "which agent ran that migration"
// and "where did I see that error" are the same question as "who said this",
// and both of those live in tool records rather than prose.
func searchableParts(cl convLine) []string {
	var blocks []convBlock
	if err := json.Unmarshal(cl.Message.Content, &blocks); err != nil {
		// Content is a bare string on some records.
		var s string
		if json.Unmarshal(cl.Message.Content, &s) == nil && s != "" {
			return []string{s}
		}
		return nil
	}

	out := make([]string, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case "text":
			out = append(out, b.Text)
		case "thinking":
			out = append(out, b.Thinking)
		case "tool_use":
			out = append(out, b.Name+" — "+summariseTool(b.Name, b.Input))
			if len(b.Input) > 0 && len(b.Input) < 64<<10 {
				out = append(out, string(b.Input))
			}
		case "tool_result":
			if s := flattenResult(b.Content); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

// snippetAround takes a readable window either side of the match.
func snippetAround(s string, at, n int) string {
	s = strings.Join(strings.Fields(strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(s)), " ")
	// The offsets were measured before the whitespace was squeezed, so find it
	// again in the flattened text rather than trusting them.
	low := strings.ToLower(s)
	needle := ""
	if at >= 0 && at+n <= len(low) {
		needle = low[at : at+n]
	}
	if i := strings.Index(low, needle); needle != "" && i >= 0 {
		at = i
	} else {
		at = 0
	}

	start, end := at-snippetPad, at+n+snippetPad
	if start < 0 {
		start = 0
	}
	if end > len(s) {
		end = len(s)
	}
	// Do not cut a rune in half.
	for start > 0 && !utf8Start(s[start]) {
		start--
	}
	for end < len(s) && !utf8Start(s[end]) {
		end++
	}
	out := s[start:end]
	if start > 0 {
		out = "…" + out
	}
	if end < len(s) {
		out += "…"
	}
	return out
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

func cap4k(s string) string {
	if len(s) <= textCap {
		return s
	}
	end := textCap
	for end > 0 && !utf8Start(s[end]) {
		end--
	}
	return s[:end] + "…"
}
