package session

import (
	"testing"
	"time"
)

// The confirmation is the real guard against benching a healthy account, so its
// judgement is pinned here case by case.
func TestLimitConfirmed(t *testing.T) {
	const quiet = settleGrace + time.Second

	cases := []struct {
		name        string
		ended       bool
		before      int64
		after       int64
		sinceOutput time.Duration
		want        bool
	}{
		{
			name:        "silent and idle after the message is a real limit",
			before:      50_000,
			after:       50_000,
			sinceOutput: quiet,
			want:        true,
		},
		{
			name:        "the agent kept working, so it was only content on screen",
			before:      50_000,
			after:       50_320,
			sinceOutput: quiet,
			want:        false,
		},
		{
			name:        "still printing, so nothing has stopped",
			before:      50_000,
			after:       50_000,
			sinceOutput: 200 * time.Millisecond,
			want:        false,
		},
		{
			name:        "process already gone leaves nothing to hand over",
			ended:       true,
			before:      50_000,
			after:       50_000,
			sinceOutput: quiet,
			want:        false,
		},
		{
			// The exact false positive that benched a healthy account: the words
			// appeared in text the agent was displaying, mid-turn.
			name:        "words displayed mid-turn while tokens climb",
			before:      1_000,
			after:       4_800,
			sinceOutput: 50 * time.Millisecond,
			want:        false,
		},
	}

	for _, c := range cases {
		got := limitConfirmed(c.ended, c.before, c.after, c.sinceOutput)
		if got != c.want {
			t.Errorf("%s: limitConfirmed(ended=%v, %d->%d, quiet=%v) = %v, want %v",
				c.name, c.ended, c.before, c.after, c.sinceOutput, got, c.want)
		}
	}
}

// An apostrophe is not a quotation mark. Real CLI notices use contractions, and
// treating them as quoted prose made the detector miss the genuine article.
func TestLooksQuoted(t *testing.T) {
	quoted := []string{
		`no real "usage limit reached" has occurred`,
		"regexp.MustCompile(`usage limit reached`)",
		`README says 'rate limit exceeded' should be handled`,
		`the docs call it “usage limit reached”`,
		`trailing apostrophe at the end '`,
	}
	for _, s := range quoted {
		if !looksQuoted(s) {
			t.Errorf("expected quoted: %q", s)
		}
	}

	plain := []string{
		"You've hit your usage limit.",
		"Claude usage limit reached. Your limit will reset at 3pm.",
		"It's reset now",
		"rate limit exceeded",
		"",
	}
	for _, s := range plain {
		if looksQuoted(s) {
			t.Errorf("expected not quoted: %q", s)
		}
	}
}
