package session

import "testing"

// Three real terminal captures, one for each thing that happens to a prompt.

// stuckTail: the text arrived but the return was swallowed, so it is sitting on
// the prompt line, typed and unsent.
const stuckTail = "\x1b[>0q\x1b[>4m\x1b[<u\r\n" +
	" ▐▛███▛█Claude Code v2.1.260\r\n" +
	"▝▜██████▀ Haiku 4.5 · Claude Max\r\n" +
	"  ▝▝ ▝▝  ~\\Documents\\Projects\\go-ai-team\r\n" +
	"───────────────\r\n" +
	"❯ Read the image at C:/Users/TITO/.goaiteam/pastes/probe/cross.png and tell me in one line what shape and colours you see.\r\n" +
	"───────────────\r\n" +
	"/rc connecting…⏸ manual mode on\r\n" +
	"go-ai-team (main) Haiku 4.5 $0.00 5h:19% wk:21%"

// missingTail: the paste never arrived at all. The composer is showing its own
// placeholder suggestion, and the prompt is nowhere on screen. This is the case
// that pressing Enter again cannot fix.
const missingTail = "\x1b[>0q\x1b[>4m\x1b[<u ▐▛███▛█ClaudeCodev2.1.260\r\n" +
	"▝▜██████▀Haiku4.5·ClaudeMax\r\n" +
	" ▝▝▝▝~\\Documents\\Projects\\go-ai-team───────────────\r\n" +
	"❯ Try \"fix type check errors\"\r\n" +
	"───────────────⏵⏵auto mode on (shift+tab to cycle)/rcconnecting…\r\n" +
	"go-ai-team (main) Haiku 4.5 $0.00"

// sentTail: the CLI took the message. It is echoed above, and the composer is
// empty again.
const sentTail = "───────────────\r\n" +
	"> Read the image at C:/Users/TITO/.goaiteam/pastes/probe/cross.png and tell me in one line what shape and colours you see.\r\n" +
	"───────────────\r\n" +
	"❯ \r\n" +
	"───────────────\r\n" +
	"✶ Cooking… (3s · ↓ 38 tokens)\r\n" +
	"go-ai-team (main) Haiku 4.5 $0.00"

const prompt = "Read the image at C:/Users/TITO/.goaiteam/pastes/probe/cross.png and tell me in one line what shape and colours you see."

func TestDeliveryState(t *testing.T) {
	cases := []struct {
		name string
		tail string
		text string
		want delivery
	}{
		{"typed but unsent", stuckTail, prompt, deliveryTyped},
		{"never arrived", missingTail, prompt, deliveryMissing},
		{"taken by the CLI", sentTail, prompt, deliverySent},
		// The composer wraps and re-indents what it holds, so whitespace in the
		// capture must not decide the answer.
		{
			"wrapped in the composer",
			"❯ Read the image at C:/Users/TITO/.goaiteam/pastes/\r\n     probe/cross.png and tell me in one\r\n",
			prompt, deliveryTyped,
		},
		// Nothing on screen at all is "never arrived", not "sent".
		{"blank terminal", "", prompt, deliveryMissing},
		{"unrelated output", "building...\r\ndone\r\n", prompt, deliveryMissing},
		// An empty prompt is never worth retyping or resubmitting.
		{"empty text", stuckTail, "   ", deliverySent},
	}
	for _, c := range cases {
		if got := deliveryState(c.tail, c.text); got != c.want {
			t.Errorf("%s: deliveryState = %v, want %v", c.name, got, c.want)
		}
	}
}

// The distinction that matters most: a prompt that never arrived must not be
// mistaken for one that was sent, or the message is lost with no sign of it.
func TestDeliveryMissingIsNotSent(t *testing.T) {
	if deliveryState(missingTail, prompt) == deliverySent {
		t.Fatal("a prompt that never arrived was reported as sent; the message would be silently lost")
	}
}

func TestSquash(t *testing.T) {
	if got := squash("  a b\tc\r\nd "); got != "abcd" {
		t.Errorf("squash = %q, want abcd", got)
	}
}
