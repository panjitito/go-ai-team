package session

import (
	"testing"
	"time"
)

func TestDetectLimit_Fires(t *testing.T) {
	cases := []string{
		"Claude usage limit reached. Your limit will reset at 3pm.",
		"Claude usage limit reached · resets at 15:00",
		"You've hit your usage limit.",
		"You have reached your limit for this window",
		"5-hour limit reached",
		"Weekly limit reached — try again in 2 hours",
		"Error: rate limit exceeded",
		"quota exceeded for this account",
		"You are out of credits",
		"HTTP 429 - Too Many Requests",
		// With ANSI colouring, which is how it really arrives.
		"\x1b[31mClaude usage limit reached\x1b[0m",
		// Buried in a multi-line chunk.
		"thinking...\nediting main.go\nClaude usage limit reached. resets at 9pm\n",
	}
	for _, c := range cases {
		if hit := DetectLimit(c); !hit.Matched {
			t.Errorf("expected a match for %q", c)
		}
	}
}

// These are the expensive mistakes: every one of them would spend a second
// subscription the user did not intend to spend.
func TestDetectLimit_DoesNotFire(t *testing.T) {
	cases := []string{
		"",
		"Reading file usage_limit_reached.go",
		"You are approaching your usage limit",
		"Heads up: you've used 80% of your limit",
		"Nearing the weekly limit",
		"You will reach your usage limit in about an hour",
		"About to hit your limit",
		"Quota: 45% of session used",
		"12% remaining on this window",
		"rate limiting is configured in middleware.ts",
		"// TODO: handle quota exceeded gracefully  -- approaching",
		// Our own switch announcement must never loop.
		"[Go AI Team] usage limit reached on Work. Handing this conversation to Personal.",
		"[Go AI Team] usage limit reached on Work and no other signed-in account is available.",
	}
	for _, c := range cases {
		if hit := DetectLimit(c); hit.Matched {
			t.Errorf("false positive on %q (matched line: %q)", c, hit.Line)
		}
	}
}

func TestDetectLimit_ParsesAbsoluteReset(t *testing.T) {
	hit := DetectLimit("Claude usage limit reached. Your limit will reset at 11pm.")
	if !hit.Matched {
		t.Fatal("expected a match")
	}
	if !hit.HasReset {
		t.Fatal("expected a reset time to be parsed")
	}
	if hit.ResetAt.Hour() != 23 {
		t.Errorf("ResetAt hour = %d, want 23", hit.ResetAt.Hour())
	}
	if !hit.ResetAt.After(time.Now()) {
		t.Error("ResetAt should be in the future")
	}
}

func TestDetectLimit_ParsesRelativeReset(t *testing.T) {
	hit := DetectLimit("Weekly limit reached. Try again in 45 minutes.")
	if !hit.Matched || !hit.HasReset {
		t.Fatal("expected a match with a reset")
	}
	if hit.BenchFor < 44*time.Minute || hit.BenchFor > 46*time.Minute {
		t.Errorf("BenchFor = %v, want ~45m", hit.BenchFor)
	}
}

func TestDetectLimit_FallsBackToDefaultBench(t *testing.T) {
	hit := DetectLimit("rate limit exceeded")
	if !hit.Matched {
		t.Fatal("expected a match")
	}
	if hit.HasReset {
		t.Error("no reset time was present, HasReset should be false")
	}
	if hit.BenchFor != DefaultBench {
		t.Errorf("BenchFor = %v, want %v", hit.BenchFor, DefaultBench)
	}
}

func TestSplitArgs(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"--verbose", []string{"--verbose"}},
		{"--model opus-4.8", []string{"--model", "opus-4.8"}},
		{`--append-system-prompt "be terse"`, []string{"--append-system-prompt", "be terse"}},
		{"  --a   --b  ", []string{"--a", "--b"}},
		{`--path 'C:\Program Files\x'`, []string{"--path", `C:\Program Files\x`}},
	}
	for _, c := range cases {
		got := splitArgs(c.in)
		if len(got) != len(c.want) {
			t.Errorf("splitArgs(%q) = %#v, want %#v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("splitArgs(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

func TestRingBuffer(t *testing.T) {
	r := newRing(10)
	if got := string(r.Snapshot()); got != "" {
		t.Errorf("empty ring = %q", got)
	}
	_, _ = r.Write([]byte("abc"))
	if got := string(r.Snapshot()); got != "abc" {
		t.Errorf("got %q, want abc", got)
	}
	_, _ = r.Write([]byte("defghij"))
	if got := string(r.Snapshot()); got != "abcdefghij" {
		t.Errorf("got %q, want abcdefghij", got)
	}
	// Overflow keeps the tail.
	_, _ = r.Write([]byte("KL"))
	if got := string(r.Snapshot()); got != "cdefghijKL" {
		t.Errorf("got %q, want cdefghijKL", got)
	}
	// A single write larger than the ring keeps only its tail.
	_, _ = r.Write([]byte("0123456789ABCDE"))
	if got := string(r.Snapshot()); got != "56789ABCDE" {
		t.Errorf("got %q, want 56789ABCDE", got)
	}
}
