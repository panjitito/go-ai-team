// Package claudefs reads the files Claude Code already writes to disk.
//
// Nothing here proxies the API or instruments the CLI. Every number Go AI Team
// shows is a faster, more visible reading of data Anthropic has already saved
// locally, which is what keeps the token meter exact and entirely offline.
//
// Layout of a Claude config directory (the thing CLAUDE_CONFIG_DIR points at):
//
//	.credentials.json                              OAuth tokens
//	sessions/<pid>.json                            live session metafile
//	projects/<encoded-cwd>/<sessionId>.jsonl       full transcript
package claudefs

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// EncodeCWD converts a working directory into the folder name Claude uses under
// projects/. Every character outside [A-Za-z0-9-] becomes a dash, and runs are
// never collapsed, so the mapping is one dash per replaced character.
//
// Verified against real transcript directories on this machine:
//
//	C:\Users\TITO\Documents\Projects       -> C--Users-TITO-Documents-Projects
//	...\Android\Apps\list_subs             -> ...-Android-Apps-list-subs
//	...\Projects\Server 74 (INTI)          -> ...-Projects-Server-74--INTI-
//	...\Projects\Alpenperkasa.id           -> ...-Projects-Alpenperkasa-id
//
// Case is preserved, including the drive letter: a cwd of c:\ yields c--.
//
// This is a fast path only. Claude owns this rule and could change it, so the
// authoritative transcript lookup is FindTranscript, which searches by session
// id and does not depend on the encoding being right.
func EncodeCWD(cwd string) string {
	var b strings.Builder
	b.Grow(len(cwd))
	for _, r := range cwd {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// SessionMeta is the contents of sessions/<pid>.json. It is the bridge between
// a process we spawned and the transcript that process is writing.
type SessionMeta struct {
	PID        int    `json:"pid"`
	SessionID  string `json:"sessionId"`
	CWD        string `json:"cwd"`
	StartedAt  int64  `json:"startedAt"`
	Version    string `json:"version"`
	Kind       string `json:"kind"`
	Entrypoint string `json:"entrypoint"`
	Name       string `json:"name"`

	// Dir is the config directory this metafile was found in, i.e. the account.
	Dir string `json:"-"`
}

// CredentialsPath returns the OAuth credentials file for a config dir.
func CredentialsPath(dir string) string { return filepath.Join(dir, ".credentials.json") }

// AuthState describes how usable an account's credentials actually are.
//
// The distinction matters more than it looks. Logging out of Claude Code leaves
// .credentials.json behind with its metadata intact — subscription type,
// organisation, token expiry — but with the token strings blanked. So the file
// existing proves nothing, and treating it as proof produces two bad outcomes:
// a dashboard that claims an account is ready when it is not, and an auto-switch
// that hands a live conversation to a dead account at exactly the moment the
// feature is supposed to rescue it.
type AuthState string

const (
	// AuthMissing means there is no credentials file at all.
	AuthMissing AuthState = "missing"
	// AuthLoggedOut means the file is there but carries no token material.
	AuthLoggedOut AuthState = "loggedOut"
	// AuthRefreshable means only a refresh token remains, still in date, so the
	// CLI can mint a new access token by itself.
	AuthRefreshable AuthState = "refreshable"
	// AuthActive means an access token is present.
	AuthActive AuthState = "active"
	// AuthUnreadable means the file could not be parsed.
	AuthUnreadable AuthState = "unreadable"
)

// Usable reports whether an agent launched against this account can expect to
// reach the API without a fresh sign-in.
func (a AuthState) Usable() bool { return a == AuthActive || a == AuthRefreshable }

// Credentials is the non-secret summary of an account's credentials file. No
// token material is ever copied into it, so it is safe to send to the UI.
type Credentials struct {
	State            AuthState `json:"state"`
	SubscriptionType string    `json:"subscriptionType,omitempty"`
	OrganizationID   string    `json:"organizationId,omitempty"`
	ExpiresAt        time.Time `json:"expiresAt,omitempty"`
	RefreshExpiresAt time.Time `json:"refreshExpiresAt,omitempty"`
}

// credentialsFile mirrors only the fields we need. Token strings are read to
// measure whether they are present and are never retained.
type credentialsFile struct {
	ClaudeAiOauth struct {
		AccessToken           string `json:"accessToken"`
		RefreshToken          string `json:"refreshToken"`
		ExpiresAt             int64  `json:"expiresAt"`
		RefreshTokenExpiresAt int64  `json:"refreshTokenExpiresAt"`
		SubscriptionType      string `json:"subscriptionType"`
		OrganizationUUID      string `json:"organizationUuid"`
	} `json:"claudeAiOauth"`
	OrganizationUUID string `json:"organizationUuid"`
}

// epochToTime accepts either seconds or milliseconds, which is what the file
// mixes, and treats zero as "not stated" rather than as 1970.
func epochToTime(v int64) time.Time {
	if v <= 0 {
		return time.Time{}
	}
	if v > 1e11 {
		return time.UnixMilli(v)
	}
	return time.Unix(v, 0)
}

// ReadCredentials summarises an account's credentials without retaining any
// secret.
func ReadCredentials(dir string) Credentials {
	if dir == "" {
		return Credentials{State: AuthMissing}
	}
	b, err := os.ReadFile(CredentialsPath(dir))
	if err != nil {
		return Credentials{State: AuthMissing}
	}
	var f credentialsFile
	if json.Unmarshal(b, &f) != nil {
		return Credentials{State: AuthUnreadable}
	}
	o := f.ClaudeAiOauth
	c := Credentials{
		SubscriptionType: o.SubscriptionType,
		OrganizationID:   o.OrganizationUUID,
		ExpiresAt:        epochToTime(o.ExpiresAt),
		RefreshExpiresAt: epochToTime(o.RefreshTokenExpiresAt),
	}
	if c.OrganizationID == "" {
		c.OrganizationID = f.OrganizationUUID
	}

	switch {
	case strings.TrimSpace(o.AccessToken) != "":
		// An access token is present. expiresAt is often 0 even on a working
		// account, so a zero value must not be read as "expired long ago".
		c.State = AuthActive
	case strings.TrimSpace(o.RefreshToken) != "":
		if c.RefreshExpiresAt.IsZero() || c.RefreshExpiresAt.After(time.Now()) {
			c.State = AuthRefreshable
		} else {
			c.State = AuthLoggedOut
		}
	default:
		c.State = AuthLoggedOut
	}
	return c
}

// SignedIn reports whether a config directory holds credentials an agent can
// actually use. This is the signal the sign-in flow polls for, and the one the
// auto-switch fallback picker trusts.
func SignedIn(dir string) bool { return ReadCredentials(dir).State.Usable() }

// AccountLabel is a short, non-secret description of who an account is: its
// plan and a truncated organisation id. The credentials file carries no email
// address, so this is the honest answer rather than a guessed one.
func AccountLabel(dir string) string {
	c := ReadCredentials(dir)
	org := c.OrganizationID
	if len(org) > 8 {
		org = org[:8]
	}
	switch {
	case c.SubscriptionType != "" && org != "":
		return c.SubscriptionType + " · org " + org
	case c.SubscriptionType != "":
		return c.SubscriptionType
	case org != "":
		return "org " + org
	}
	return ""
}

// ReadSessionMeta loads sessions/<pid>.json from one config directory.
func ReadSessionMeta(dir string, pid int) (*SessionMeta, error) {
	p := filepath.Join(dir, "sessions", strconv.Itoa(pid)+".json")
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var m SessionMeta
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	m.Dir = dir
	return &m, nil
}

// FindSessionMeta looks for a PID's metafile across every known account
// directory. PIDs are unique per machine, so the file exists in exactly one
// directory and the first hit is the right one. This is what keeps the token
// meter and file attribution correct no matter which account spawned the agent.
func FindSessionMeta(dirs []string, pid int) *SessionMeta {
	for _, d := range dirs {
		if d == "" {
			continue
		}
		if m, err := ReadSessionMeta(d, pid); err == nil && m.SessionID != "" {
			return m
		}
	}
	return nil
}

// SessionOwnerDir reports which config directory a process's session belongs to.
//
// It globs sessions/<pid>.* rather than looking for <pid>.json alone, because
// Claude Code writes a <pid>.<hash>.key file the instant a session starts but
// only writes the <pid>.json metafile once that session is established. Keying
// off the prefix means account attribution is available immediately — which is
// also the cheapest way to confirm a spawn really landed in the account we
// bound it to.
func SessionOwnerDir(dirs []string, pid int) (string, bool) {
	needle := strconv.Itoa(pid)
	for _, d := range dirs {
		if d == "" {
			continue
		}
		matches, err := filepath.Glob(filepath.Join(d, "sessions", needle+".*"))
		if err == nil && len(matches) > 0 {
			return d, true
		}
	}
	return "", false
}

// TokenStats is the per-session breakdown shown in the meter. The four counters
// are billed separately, which is why they are never summed into one number
// before the UI asks for it.
type TokenStats struct {
	InputTokens  int64 `json:"inputTokens"`
	OutputTokens int64 `json:"outputTokens"`
	CacheWrite   int64 `json:"cacheWrite"`
	CacheRead    int64 `json:"cacheRead"`

	Messages      int `json:"messages"`
	UserTurns     int `json:"userTurns"`
	AssistantTurn int `json:"assistantTurns"`
	ToolUses      int `json:"toolUses"`

	Models []string `json:"models"`

	FirstAt time.Time `json:"firstAt,omitempty"`
	LastAt  time.Time `json:"lastAt,omitempty"`

	SessionID string `json:"sessionId,omitempty"`
	Path      string `json:"path,omitempty"`
}

// Total is the headline number: every counter added up.
func (t TokenStats) Total() int64 {
	return t.InputTokens + t.OutputTokens + t.CacheWrite + t.CacheRead
}

// CacheHitRate is the share of input-side tokens served from cache. Cache reads
// cost roughly a tenth of fresh input, so this single percentage is the
// strongest lever on what a long session costs.
func (t TokenStats) CacheHitRate() float64 {
	den := t.InputTokens + t.CacheRead + t.CacheWrite
	if den == 0 {
		return 0
	}
	return float64(t.CacheRead) / float64(den) * 100
}

// Duration is the wall time between the first and last message.
func (t TokenStats) Duration() time.Duration {
	if t.FirstAt.IsZero() || t.LastAt.IsZero() {
		return 0
	}
	return t.LastAt.Sub(t.FirstAt)
}

// transcript line shapes we care about. Everything else is ignored, so an
// unknown future field can never break the parse.
type jsonlLine struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	Timestamp string `json:"timestamp"`
	Message   struct {
		Role    string          `json:"role"`
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
		Usage   struct {
			InputTokens        int64 `json:"input_tokens"`
			OutputTokens       int64 `json:"output_tokens"`
			CacheCreationInput int64 `json:"cache_creation_input_tokens"`
			CacheReadInput     int64 `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// TranscriptPath returns the encoded-path guess for a session's transcript.
func TranscriptPath(dir, cwd, sessionID string) string {
	return filepath.Join(dir, "projects", EncodeCWD(cwd), sessionID+".jsonl")
}

// FindTranscript locates a session's transcript inside one account directory.
//
// It tries the encoded cwd first because that is a single stat, then falls back
// to scanning projects/*/ for <sessionId>.jsonl. The fallback is what makes this
// correct rather than merely usually-correct: the session id is unique and
// authoritative, so a change to Claude's folder-naming rule degrades this to a
// slower lookup instead of a wrong answer.
func FindTranscript(dir, cwd, sessionID string) (string, bool) {
	if dir == "" || sessionID == "" {
		return "", false
	}
	if cwd != "" {
		guess := TranscriptPath(dir, cwd, sessionID)
		if st, err := os.Stat(guess); err == nil && !st.IsDir() {
			return guess, true
		}
	}
	matches, err := filepath.Glob(filepath.Join(dir, "projects", "*", sessionID+".jsonl"))
	if err != nil || len(matches) == 0 {
		return "", false
	}
	return matches[0], true
}

// ParseTranscript walks one session's JSONL and sums the usage payload Anthropic
// writes per message. There is no estimation here: the four counters come
// straight off the wire record.
func ParseTranscript(path string) (TokenStats, error) {
	f, err := os.Open(path)
	if err != nil {
		return TokenStats{Path: path}, err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return TokenStats{Path: path}, err
	}

	// Resume where the last parse stopped. See incremental.go: a transcript is
	// append-only and can be hundreds of megabytes, and this runs on a poll.
	r := loadResume(path, st.Size())
	if r == nil {
		r = &resume{models: map[string]bool{}}
	}
	if r.off >= st.Size() {
		// Nothing new since last time.
		out := r.stats
		out.Path = path
		out.Models = append([]string(nil), r.stats.Models...)
		return out, nil
	}
	if r.off > 0 {
		if _, err := f.Seek(r.off, io.SeekStart); err != nil {
			return TokenStats{Path: path}, err
		}
	}

	t := r.stats
	t.Path = path
	seenModel := r.models
	off := r.off

	// Read with an explicit reader rather than a Scanner: the offset has to be
	// exact so the next parse resumes on a record boundary, and a Scanner does
	// not report how many bytes it consumed.
	br := bufio.NewReaderSize(f, 256*1024)
	for {
		line, readErr := br.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] != '\n' {
			// A record still being written. Leave it for next time rather than
			// counting half of it.
			break
		}
		if len(line) == 0 {
			break
		}
		off += int64(len(line))

		b := trimEOL(line)
		if len(b) == 0 || b[0] != '{' {
			if readErr != nil {
				break
			}
			continue
		}
		var l jsonlLine
		if json.Unmarshal(b, &l) != nil {
			if readErr != nil {
				break
			}
			continue
		}
		if l.SessionID != "" {
			t.SessionID = l.SessionID
		}
		if ts, err := time.Parse(time.RFC3339Nano, l.Timestamp); err == nil {
			if t.FirstAt.IsZero() {
				t.FirstAt = ts
			}
			t.LastAt = ts
		}

		switch l.Type {
		case "user":
			t.Messages++
			t.UserTurns++
		case "assistant":
			t.Messages++
			t.AssistantTurn++
			u := l.Message.Usage
			t.InputTokens += u.InputTokens
			t.OutputTokens += u.OutputTokens
			t.CacheWrite += u.CacheCreationInput
			t.CacheRead += u.CacheReadInput
			if m := l.Message.Model; m != "" && !seenModel[m] {
				seenModel[m] = true
				t.Models = append(t.Models, m)
			}
			t.ToolUses += countToolUses(l.Message.Content)
		}
		if readErr != nil {
			break
		}
	}

	sort.Strings(t.Models)
	saveResume(path, &resume{off: off, size: st.Size(), stats: t, models: seenModel})

	out := t
	out.Models = append([]string(nil), t.Models...)
	return out, nil
}

// trimEOL strips the line ending a reader kept.
func trimEOL(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

// countToolUses counts tool_use blocks in an assistant message's content array.
func countToolUses(raw json.RawMessage) int {
	if len(raw) == 0 || raw[0] != '[' {
		return 0
	}
	var blocks []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return 0
	}
	n := 0
	for _, b := range blocks {
		if b.Type == "tool_use" {
			n++
		}
	}
	return n
}

// LatestTranscript finds the most recently modified transcript for a working
// directory inside one account. It is the fallback used before a session
// metafile appears, so the meter has something to show on the first turn.
func LatestTranscript(dir, cwd string) (string, time.Time, bool) {
	pdir := filepath.Join(dir, "projects", EncodeCWD(cwd))
	entries, err := os.ReadDir(pdir)
	if err != nil {
		return "", time.Time{}, false
	}
	var best string
	var bestMod time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(bestMod) {
			bestMod = info.ModTime()
			best = filepath.Join(pdir, e.Name())
		}
	}
	if best == "" {
		return "", time.Time{}, false
	}
	return best, bestMod, true
}

// UserLayerEntries are the files and directories that make up a user's own
// Claude configuration. A freshly signed-in account starts with none of them,
// which is why sharing them is a deliberate, reversible switch rather than a
// copy: credentials, sessions and transcripts stay isolated either way.
var UserLayerEntries = []string{
	"CLAUDE.md",
	"settings.json",
	"commands",
	"skills",
	"agents",
	"hooks",
	"plugins",
	"output-styles",
}
