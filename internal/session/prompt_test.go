package session

import "testing"

// realIdleTail is a real capture from an agent that had just finished a turn and
// was sitting at its empty composer. Nothing is being asked. The old detector
// fired on it anyway, and the UI told the user their idle agent was waiting for
// an answer — over a terminal that plainly showed it was not.
//
// Two things in here used to match: the ">" that begins every quoted message
// Claude Code renders, and the "❯" of the empty composer itself.
const realIdleTail = "" +
	"✻ recap: Goal is testing MIS Dashboard changes in a dedicated browser.\r\n" +
	"> Done sign in to claude extension. The chrome profile is [Image #3]\r\n" +
	"  └ [Image #3]\r\n" +
	"  Called claude-in-chrome (ctrl+o to expand)\r\n" +
	"● Artifact comment monitor for https://claude.ai/code/artifact/28956088 didn't resume its\r\n" +
	"  automatic replies here: this conversation is also open in another terminal\r\n" +
	"● Remote Control not started here · another Claude Code on this machine already has it\r\n" +
	"\r\n" +
	"> \r\n" +
	"  mis-dashboard Opus 5 (1M context) ctx:67% $880.76 5h:10% wk:25%\r\n" +
	"  ⏵⏵ bypass permissions on (shift+tab to cycle) · ← 1 agent\r\n"

func TestNeedsTerminal_IdleAgentIsNotAsking(t *testing.T) {
	if need, title := NeedsTerminal(realIdleTail); need {
		t.Errorf("false positive on an idle agent: %q", title)
	}
}

// Content an agent happens to be displaying must not look like a question.
func TestNeedsTerminal_ContentOnScreenIsNotAPrompt(t *testing.T) {
	cases := []string{
		"> Please review the plan and tell me if you want to proceed\r\n> \r\n",
		"❯ \r\n",                              // the empty composer, always on screen
		"❯ fix the type errors\r\n",           // text typed into the composer
		"reading docs about /login flows\r\n", // prose mentioning sign-in
		"| Option | Meaning |\r\n| 1. | Yes |\r\n",
		"the script prints (y/N) when run\r\n> \r\n",
		"",
	}
	for _, c := range cases {
		if need, title := NeedsTerminal(c); need {
			t.Errorf("false positive on %q -> %q", c, title)
		}
	}
}

// The real prompts still have to be caught, or the conversation view looks
// frozen with no explanation.
func TestNeedsTerminal_CatchesRealPrompts(t *testing.T) {
	cases := []struct{ tail, wantIn string }{
		{
			"Do you trust the files in this folder?\r\n" +
				"❯ 1. Yes, proceed\r\n  2. No, exit\r\n" +
				"Enter to confirm · Esc to cancel\r\n",
			"trust",
		},
		{
			"Do you want to make this edit to main.go?\r\n" +
				"❯ 1. Yes\r\n  2. No, tell Claude what to do differently\r\n",
			"permission",
		},
		{
			"Paste the code here:\r\n",
			"sign in",
		},
	}
	for _, c := range cases {
		need, title := NeedsTerminal(c.tail)
		if !need {
			t.Errorf("missed a real prompt: %q", c.tail)
			continue
		}
		if c.wantIn != "" && !contains(title, c.wantIn) {
			t.Errorf("title for %q = %q, want it to mention %q", c.tail, title, c.wantIn)
		}
	}
}

// An answered prompt scrolls away. Matching it forever would pin the banner up
// over a working agent.
func TestNeedsTerminal_OnlyTheCurrentFrame(t *testing.T) {
	old := "Do you trust the files in this folder?\r\n❯ 1. Yes, proceed\r\n"
	padding := ""
	for i := 0; i < 60; i++ {
		padding += "the agent carried on working after that was answered\r\n"
	}
	if need, _ := NeedsTerminal(old + padding); need {
		t.Error("a prompt answered long ago should not still raise the banner")
	}
	if need, _ := NeedsTerminal(padding + old); !need {
		t.Error("a prompt in the current frame should raise the banner")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if equalFold(s[i:i+len(sub)], sub) {
			return true
		}
	}
	return false
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if 'A' <= x && x <= 'Z' {
			x += 'a' - 'A'
		}
		if 'A' <= y && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}
