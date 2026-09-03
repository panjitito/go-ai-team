package claudefs

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// Cases captured from real transcript directories written by Claude Code, so
// this test fails loudly if the encoding rule is ever wrong again.
func TestEncodeCWD(t *testing.T) {
	cases := []struct{ cwd, want string }{
		{`C:\Users\TITO\Documents\Projects`, "C--Users-TITO-Documents-Projects"},
		{`C:\Users\TITO\Documents\Projects\go-ai-team`, "C--Users-TITO-Documents-Projects-go-ai-team"},
		{`C:\Users\TITO\Documents\Projects\Android\Apps\list_subs`, "C--Users-TITO-Documents-Projects-Android-Apps-list-subs"},
		{`C:\Users\TITO\Documents\Projects\Server 74 (INTI)`, "C--Users-TITO-Documents-Projects-Server-74--INTI-"},
		{`C:\Users\TITO\Documents\Projects\Alpenperkasa.id`, "C--Users-TITO-Documents-Projects-Alpenperkasa-id"},
		{`C:\Users\TITO\Documents\Projects\thepapbunskyjourney.com`, "C--Users-TITO-Documents-Projects-thepapbunskyjourney-com"},
		{`c:\Users\TITO\Documents\Projects\RPA Analysis`, "c--Users-TITO-Documents-Projects-RPA-Analysis"},
		{`C:\Users\TITO\Videos\Downloads`, "C--Users-TITO-Videos-Downloads"},
		{`/home/dev/my project`, "-home-dev-my-project"},
	}
	for _, c := range cases {
		if got := EncodeCWD(c.cwd); got != c.want {
			t.Errorf("EncodeCWD(%q)\n got %q\nwant %q", c.cwd, got, c.want)
		}
	}
}

func TestTokenStatsMath(t *testing.T) {
	s := TokenStats{InputTokens: 100, OutputTokens: 50, CacheWrite: 200, CacheRead: 700}
	if got := s.Total(); got != 1050 {
		t.Errorf("Total() = %d, want 1050", got)
	}
	// 700 cache reads out of 1000 input-side tokens.
	if got := s.CacheHitRate(); got < 69.9 || got > 70.1 {
		t.Errorf("CacheHitRate() = %v, want ~70", got)
	}
	var zero TokenStats
	if got := zero.CacheHitRate(); got != 0 {
		t.Errorf("CacheHitRate() on empty = %v, want 0", got)
	}
}

// writeCreds drops a credentials file into a temp dir.
func writeCreds(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The husk case is the one that matters: logging out of Claude Code leaves the
// file in place with its metadata intact and the tokens blanked. Reading that as
// "signed in" would make the dashboard lie and would let auto-switch hand a live
// conversation to a dead account.
func TestReadCredentials_LoggedOutHusk(t *testing.T) {
	future := time.Now().Add(400 * time.Hour).UnixMilli()
	dir := writeCreds(t, `{"claudeAiOauth":{
		"accessToken":"","refreshToken":"","expiresAt":0,
		"refreshTokenExpiresAt":`+strconv.FormatInt(future, 10)+`,
		"subscriptionType":"max","organizationUuid":"9406d938-87c4-43a5-9512-1fee9d61fa90"}}`)

	c := ReadCredentials(dir)
	if c.State != AuthLoggedOut {
		t.Errorf("State = %q, want %q", c.State, AuthLoggedOut)
	}
	if c.State.Usable() {
		t.Error("a logged-out husk must not be reported as usable")
	}
	if SignedIn(dir) {
		t.Error("SignedIn must be false for a husk with blank tokens")
	}
	// Metadata should still be surfaced, since it is what tells the user which
	// account this directory used to hold.
	if c.SubscriptionType != "max" {
		t.Errorf("SubscriptionType = %q, want max", c.SubscriptionType)
	}
	if got := AccountLabel(dir); got != "max · org 9406d938" {
		t.Errorf("AccountLabel = %q", got)
	}
}

// A working account often has expiresAt of 0 while holding a real access token,
// so zero must never be read as "expired in 1970".
func TestReadCredentials_ActiveWithZeroExpiry(t *testing.T) {
	dir := writeCreds(t, `{"claudeAiOauth":{
		"accessToken":"sk-ant-oat-xxxxxxxxxxxx","refreshToken":"sk-ant-ort-xxxxxxxxxxxx",
		"expiresAt":0,"subscriptionType":"max"}}`)
	c := ReadCredentials(dir)
	if c.State != AuthActive {
		t.Errorf("State = %q, want %q", c.State, AuthActive)
	}
	if !SignedIn(dir) {
		t.Error("an account holding an access token must be usable")
	}
}

func TestReadCredentials_RefreshableAndExpired(t *testing.T) {
	future := time.Now().Add(100 * time.Hour).UnixMilli()
	past := time.Now().Add(-100 * time.Hour).UnixMilli()

	live := writeCreds(t, `{"claudeAiOauth":{"accessToken":"","refreshToken":"r",
		"refreshTokenExpiresAt":`+strconv.FormatInt(future, 10)+`}}`)
	if got := ReadCredentials(live).State; got != AuthRefreshable {
		t.Errorf("live refresh token: State = %q, want %q", got, AuthRefreshable)
	}
	if !SignedIn(live) {
		t.Error("a valid refresh token should count as usable")
	}

	dead := writeCreds(t, `{"claudeAiOauth":{"accessToken":"","refreshToken":"r",
		"refreshTokenExpiresAt":`+strconv.FormatInt(past, 10)+`}}`)
	if got := ReadCredentials(dead).State; got != AuthLoggedOut {
		t.Errorf("expired refresh token: State = %q, want %q", got, AuthLoggedOut)
	}
}

func TestReadCredentials_MissingAndUnreadable(t *testing.T) {
	if got := ReadCredentials(t.TempDir()).State; got != AuthMissing {
		t.Errorf("no file: State = %q, want %q", got, AuthMissing)
	}
	if got := ReadCredentials("").State; got != AuthMissing {
		t.Errorf("empty dir: State = %q, want %q", got, AuthMissing)
	}
	bad := writeCreds(t, `{not json`)
	if got := ReadCredentials(bad).State; got != AuthUnreadable {
		t.Errorf("bad json: State = %q, want %q", got, AuthUnreadable)
	}
}

func TestEpochToTime(t *testing.T) {
	if !epochToTime(0).IsZero() {
		t.Error("zero must mean 'not stated', not 1970")
	}
	if !epochToTime(-5).IsZero() {
		t.Error("negative must mean 'not stated'")
	}
	ms := epochToTime(1789980000000)
	if ms.Year() != 2026 {
		t.Errorf("millisecond epoch parsed as %v", ms)
	}
	s := epochToTime(1789980000)
	if s.Year() != 2026 {
		t.Errorf("second epoch parsed as %v", s)
	}
}
