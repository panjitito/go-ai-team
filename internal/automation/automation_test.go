package automation

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/panjitito/go-ai-team/internal/store"
)

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", s, time.Local)
	if err != nil {
		panic(err)
	}
	return t
}

func TestNextRun(t *testing.T) {
	cases := []struct {
		name string
		sc   store.Schedule
		from string
		want string
	}{
		{"every 15 minutes", store.Schedule{Every: "minutes", N: 15}, "2026-03-10 09:07:00", "2026-03-10 09:22:00"},
		{"minutes defaults when N is 0", store.Schedule{Every: "minutes"}, "2026-03-10 09:07:00", "2026-03-10 09:22:00"},
		{"hourly later this hour", store.Schedule{Every: "hourly", Minute: 15}, "2026-03-10 09:07:00", "2026-03-10 09:15:00"},
		{"hourly rolls to next hour", store.Schedule{Every: "hourly", Minute: 5}, "2026-03-10 09:07:00", "2026-03-10 10:05:00"},
		{"daily later today", store.Schedule{Every: "daily", Hour: 18, Minute: 30}, "2026-03-10 09:00:00", "2026-03-10 18:30:00"},
		{"daily rolls to tomorrow", store.Schedule{Every: "daily", Hour: 8, Minute: 0}, "2026-03-10 09:00:00", "2026-03-11 08:00:00"},
		// 2026-03-10 is a Tuesday.
		{"weekly same day later", store.Schedule{Every: "weekly", Wday: 2, Hour: 18}, "2026-03-10 09:00:00", "2026-03-10 18:00:00"},
		{"weekly same day passed", store.Schedule{Every: "weekly", Wday: 2, Hour: 8}, "2026-03-10 09:00:00", "2026-03-17 08:00:00"},
		{"weekly forward in week", store.Schedule{Every: "weekly", Wday: 5, Hour: 9}, "2026-03-10 09:00:00", "2026-03-13 09:00:00"},
		{"weekly wraps to next week", store.Schedule{Every: "weekly", Wday: 1, Hour: 9}, "2026-03-10 09:00:00", "2026-03-16 09:00:00"},
		{"monthly later this month", store.Schedule{Every: "monthly", Mday: 20, Hour: 9}, "2026-03-10 09:00:00", "2026-03-20 09:00:00"},
		{"monthly rolls over", store.Schedule{Every: "monthly", Mday: 5, Hour: 9}, "2026-03-10 09:00:00", "2026-04-05 09:00:00"},
	}
	for _, c := range cases {
		got := NextRun(&c.sc, at(c.from))
		want := at(c.want)
		if !got.Equal(want) {
			t.Errorf("%s: NextRun(%s) = %s, want %s", c.name, c.from,
				got.Format("2006-01-02 15:04:05"), want.Format("2006-01-02 15:04:05"))
		}
	}
}

// "The 31st of every month" must still fire in a short month rather than
// silently rolling into the next one and skipping February entirely.
func TestNextRunClampsShortMonths(t *testing.T) {
	sc := store.Schedule{Every: "monthly", Mday: 31, Hour: 9}
	got := NextRun(&sc, at("2026-01-31 10:00:00"))
	if got.Month() != time.February || got.Day() != 28 {
		t.Errorf("got %s, want 28 February", got.Format("2006-01-02 15:04"))
	}
}

// Whatever the cadence, the next run must always be strictly in the future, or
// the tick loop would fire the same schedule forever.
func TestNextRunAlwaysAdvances(t *testing.T) {
	from := at("2026-03-10 09:00:00")
	for _, every := range []string{"minutes", "hourly", "daily", "weekly", "monthly", "nonsense"} {
		sc := store.Schedule{Every: every, N: 1, Minute: 0, Hour: 9, Wday: 2, Mday: 10}
		got := NextRun(&sc, from)
		if !got.After(from) {
			t.Errorf("%s: NextRun did not advance (got %s)", every, got)
		}
	}
}

func TestDescribe(t *testing.T) {
	cases := []struct {
		sc   store.Schedule
		want string
	}{
		{store.Schedule{Every: "minutes", N: 1}, "every minute"},
		{store.Schedule{Every: "minutes", N: 30}, "every 30 minutes"},
		{store.Schedule{Every: "hourly", Minute: 5}, "every hour at :05"},
		{store.Schedule{Every: "daily", Hour: 8, Minute: 0}, "every day at 08:00"},
		{store.Schedule{Every: "weekly", Wday: 1, Hour: 9, Minute: 0}, "Mondays at 09:00"},
		{store.Schedule{Every: "monthly", Mday: 1, Hour: 9, Minute: 0}, "day 1 each month at 09:00"},
	}
	for _, c := range cases {
		if got := Describe(&c.sc); got != c.want {
			t.Errorf("Describe = %q, want %q", got, c.want)
		}
	}
}

// ---------- webhook ----------

func TestFlattenAndInterpolate(t *testing.T) {
	body := []byte(`{"action":"opened","number":42,"draft":false,
		"pull_request":{"title":"Add rate limiting","user":{"login":"alice"}},
		"labels":["bug","urgent"]}`)
	vars := flatten(body, map[string]string{"x-github-event": "pull_request"})

	checks := map[string]string{
		"event.action":                  "opened",
		"event.number":                  "42",
		"event.draft":                   "false",
		"event.pull_request.title":      "Add rate limiting",
		"event.pull_request.user.login": "alice",
		"event.labels.0":                "bug",
		"event.labels.count":            "2",
		"header.x-github-event":         "pull_request",
	}
	for k, want := range checks {
		if got := vars[k]; got != want {
			t.Errorf("vars[%q] = %q, want %q", k, got, want)
		}
	}

	tmpl := "PR #{{event.number}} by {{event.pull_request.user.login}}: {{event.pull_request.title}}"
	want := "PR #42 by alice: Add rate limiting"
	if got := Interpolate(tmpl, vars); got != want {
		t.Errorf("Interpolate = %q, want %q", got, want)
	}

	// A mistyped variable must stay visible rather than blanking out, so the
	// mistake is obvious in the agent's first message.
	if got := Interpolate("x {{event.nope}} y", vars); got != "x {{event.nope}} y" {
		t.Errorf("unknown var was not preserved: %q", got)
	}
	// An unterminated reference must not eat the rest of the prompt.
	if got := Interpolate("tail {{unclosed", vars); got != "tail {{unclosed" {
		t.Errorf("unterminated reference mangled: %q", got)
	}
}

func TestMatch(t *testing.T) {
	vars := map[string]string{
		"event.action": "opened",
		"event.title":  "Fix the login redirect loop",
		"event.draft":  "false",
		"event.empty":  "",
	}
	cases := []struct {
		expr string
		want bool
	}{
		{"", true},
		{`event.action == "opened"`, true},
		{`event.action == "closed"`, false},
		{`event.action != "closed"`, true},
		{`event.title contains "login"`, true},
		{`event.title contains "logout"`, false},
		{`event.title startswith "Fix"`, true},
		{`event.title endswith "loop"`, true},
		{`event.action exists`, true},
		{`event.empty exists`, false},
		{`event.missing exists`, false},
		// A missing key must not satisfy an equality test.
		{`event.missing == "opened"`, false},
		{`event.action == "opened" && event.draft == "false"`, true},
		{`event.action == "opened" && event.draft == "true"`, false},
		{`event.action == "closed" || event.action == "opened"`, true},
		{`event.action == "closed" || event.action == "merged"`, false},
	}
	for _, c := range cases {
		got, err := Match(c.expr, vars)
		if err != nil {
			t.Errorf("Match(%q) errored: %v", c.expr, err)
			continue
		}
		if got != c.want {
			t.Errorf("Match(%q) = %v, want %v", c.expr, got, c.want)
		}
	}
	if _, err := Match("this is not a filter", vars); err == nil {
		t.Error("an unreadable filter should be an error, not a silent false")
	}
}

func TestVerifySignature(t *testing.T) {
	secret := "s3cret"
	body := []byte(`{"action":"opened"}`)

	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	good := hex.EncodeToString(m.Sum(nil))

	// GitHub sha256, the common case.
	if err := verify(secret, body, map[string]string{"x-hub-signature-256": "sha256=" + good}); err != nil {
		t.Errorf("a valid GitHub signature was rejected: %v", err)
	}
	// Wrong digest.
	if err := verify(secret, body, map[string]string{"x-hub-signature-256": "sha256=deadbeef"}); err == nil {
		t.Error("a bad signature was accepted")
	}
	// Right digest for a different body: proves the body is really covered.
	if err := verify(secret, []byte(`{"action":"closed"}`),
		map[string]string{"x-hub-signature-256": "sha256=" + good}); err == nil {
		t.Error("a signature from a different body was accepted")
	}
	// Shared-token style.
	if err := verify(secret, body, map[string]string{"x-gitlab-token": secret}); err != nil {
		t.Errorf("a valid GitLab token was rejected: %v", err)
	}
	if err := verify(secret, body, map[string]string{"x-gitlab-token": "wrong"}); err == nil {
		t.Error("a wrong GitLab token was accepted")
	}
	// A secret is set but nothing signed the delivery: must refuse.
	if err := verify(secret, body, map[string]string{}); err == nil {
		t.Error("an unsigned delivery was accepted despite a configured secret")
	}
}

// ---------- gating, end to end ----------

type fakeLauncher struct {
	calls   int
	prompts []string
	err     error
}

func (f *fakeLauncher) LaunchTask(projectID, agentID, prompt, origin string) (string, error) {
	f.calls++
	f.prompts = append(f.prompts, prompt)
	if f.err != nil {
		return "", f.err
	}
	return "ses_fake", nil
}

type fakeNotifier struct{ kinds []string }

func (f *fakeNotifier) Notify(kind, message string, payload any) {
	f.kinds = append(f.kinds, kind)
}

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	st, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestDeliverGates(t *testing.T) {
	st := newTestStore(t)
	l := &fakeLauncher{}
	h := NewHooks(st, l, &fakeNotifier{})

	w := &store.Webhook{
		ProjectID: "prj_1", Name: "PRs", Enabled: true,
		Prompt: "Review PR {{event.number}}", Token: "tok", BurstLimit: 2,
		Filter: `event.action == "opened"`,
	}
	if err := st.AddWebhook(w); err != nil {
		t.Fatal(err)
	}

	// Filter does not match: nothing runs, and that is not an error.
	res := h.Deliver(w, []byte(`{"action":"closed","number":1}`), nil)
	if res.Accepted || l.calls != 0 {
		t.Errorf("a non-matching delivery started an agent: %+v", res)
	}

	// Matching delivery runs, with variables substituted.
	res = h.Deliver(w, []byte(`{"action":"opened","number":7}`), nil)
	if !res.Accepted || l.calls != 1 {
		t.Fatalf("a matching delivery did not run: %+v", res)
	}
	if l.prompts[0] != "Review PR 7" {
		t.Errorf("prompt = %q", l.prompts[0])
	}

	// Burst limit of 2: the second runs, the third is refused.
	res = h.Deliver(w, []byte(`{"action":"opened","number":8}`), nil)
	if !res.Accepted {
		t.Errorf("the second delivery should have run: %+v", res)
	}
	res = h.Deliver(w, []byte(`{"action":"opened","number":9}`), nil)
	if res.Accepted {
		t.Error("the burst limit did not hold")
	}
	if l.calls != 2 {
		t.Errorf("launcher was called %d times, want 2", l.calls)
	}

	// A disabled trigger never runs.
	w2, _ := st.UpdateWebhook(w.ID, func(x *store.Webhook) { x.Enabled = false })
	before := l.calls
	if res := h.Deliver(w2, []byte(`{"action":"opened"}`), nil); res.Accepted {
		t.Error("a disabled trigger ran")
	}
	if l.calls != before {
		t.Error("a disabled trigger reached the launcher")
	}
}

// A delivery that cannot start an agent must be queued, not lost, and replayed
// later — that is the whole reason an event arriving while the app is closed is
// still useful.
func TestDeliverQueuesOnLaunchFailureAndReplays(t *testing.T) {
	st := newTestStore(t)
	l := &fakeLauncher{err: errFake("no project")}
	n := &fakeNotifier{}
	h := NewHooks(st, l, n)

	w := &store.Webhook{ProjectID: "prj_gone", Name: "CI", Enabled: true, Prompt: "Look at {{event.id}}"}
	_ = st.AddWebhook(w)

	res := h.Deliver(w, []byte(`{"id":"abc"}`), nil)
	if res.Accepted || !res.Queued {
		t.Fatalf("expected the delivery to be queued: %+v", res)
	}
	if got := len(st.QueuedEvents()); got != 1 {
		t.Fatalf("queued events = %d, want 1", got)
	}

	// Now the launcher works: replay must run it and drain the queue.
	l.err = nil
	h.ReplayQueued()
	if l.calls != 2 {
		t.Errorf("launcher calls = %d, want 2 (one failed, one replayed)", l.calls)
	}
	if got := len(st.QueuedEvents()); got != 0 {
		t.Errorf("queue still holds %d events after replay", got)
	}
	if l.prompts[len(l.prompts)-1] != "Look at abc" {
		t.Errorf("replayed prompt = %q", l.prompts[len(l.prompts)-1])
	}
}

type errFake string

func (e errFake) Error() string { return string(e) }
