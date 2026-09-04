package claudefs

import (
	"os"
	"sync"
)

// Reading a growing transcript without re-reading it every time.
//
// The token meter polls each live session, and a transcript is append-only and
// can be enormous — 257MB on a real long-running session here, which took 762ms
// to parse. Doing that every few seconds, per session, means the app spends its
// life re-reading files that gained a few kilobytes.
//
// So the parse resumes. What was already counted is kept, along with the byte
// offset it was counted up to, and only the new bytes are read. The fields are
// all sums and a set of model names, so resuming is exact rather than an
// approximation.
//
// Two things make this safe on a file being written right now:
//
//   - A trailing line with no newline yet is a record mid-write. It is not
//     counted and the offset does not advance past it, so it is read properly
//     once the rest arrives.
//   - If the file ever shrinks, the assumption of append-only is broken — a
//     different session reusing the path, or a rewrite — and the whole thing is
//     parsed again from zero.

// resume is the state needed to continue a parse where it stopped.
type resume struct {
	// off is the offset of the first byte not yet counted. Always a record
	// boundary.
	off int64
	// size is the file size when that offset was reached, used to notice a file
	// that shrank.
	size   int64
	stats  TokenStats
	models map[string]bool
}

// resumeCache is small: one entry per transcript being watched. Entries are
// dropped wholesale once there are more than a session's worth, which costs one
// full re-parse and bounds the memory.
var (
	resumeMu    sync.Mutex
	resumeState = map[string]*resume{}
)

const resumeCacheMax = 64

func loadResume(path string, size int64) *resume {
	resumeMu.Lock()
	defer resumeMu.Unlock()
	r, ok := resumeState[path]
	if !ok {
		return nil
	}
	// The file shrank, so it is not the file we counted. Start again.
	if size < r.size {
		delete(resumeState, path)
		return nil
	}
	cp := &resume{off: r.off, size: r.size, stats: r.stats, models: map[string]bool{}}
	cp.stats.Models = append([]string(nil), r.stats.Models...)
	for k := range r.models {
		cp.models[k] = true
	}
	return cp
}

func saveResume(path string, r *resume) {
	resumeMu.Lock()
	defer resumeMu.Unlock()
	if len(resumeState) > resumeCacheMax {
		resumeState = map[string]*resume{}
	}
	resumeState[path] = r
}

// ForgetTranscript drops the cached parse for a path. Used when a session ends,
// so a finished agent does not hold its transcript's state forever.
func ForgetTranscript(path string) {
	resumeMu.Lock()
	delete(resumeState, path)
	resumeMu.Unlock()
	convMu.Lock()
	delete(convCache, path)
	convMu.Unlock()
}

// Caching the parsed conversation.
//
// The view polls every 1.5 seconds, and most of those polls happen while the
// transcript has not changed at all — an agent thinking, or waiting on a person.
// Re-reading and re-parsing megabytes to produce a result identical to the last
// one is pure waste, so an unchanged file returns what it returned before.
//
// Keyed on size and modification time together: either alone can miss an edit,
// and both changing is what "the CLI wrote another record" looks like.

type convCacheEntry struct {
	size  int64
	mod   int64
	limit int
	msgs  []Message
}

var (
	convMu    sync.Mutex
	convCache = map[string]*convCacheEntry{}
)

const convCacheMax = 32

func loadConv(path string, limit int) ([]Message, bool) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, false
	}
	convMu.Lock()
	defer convMu.Unlock()
	e, ok := convCache[path]
	if !ok || e.limit != limit || e.size != st.Size() || e.mod != st.ModTime().UnixNano() {
		return nil, false
	}
	return e.msgs, true
}

func saveConv(path string, limit int, msgs []Message) {
	st, err := os.Stat(path)
	if err != nil {
		return
	}
	convMu.Lock()
	defer convMu.Unlock()
	if len(convCache) > convCacheMax {
		convCache = map[string]*convCacheEntry{}
	}
	convCache[path] = &convCacheEntry{
		size: st.Size(), mod: st.ModTime().UnixNano(), limit: limit, msgs: msgs,
	}
}
