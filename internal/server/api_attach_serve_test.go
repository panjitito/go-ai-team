package server

import (
	"strings"
	"testing"

	"github.com/panjitito/go-ai-team/internal/claudefs"
)

// The two path segments this route accepts come straight off the URL and are
// joined onto a directory on disk. Anything that is not a plain name must be
// refused outright rather than cleaned up and used, because "sanitise it and
// carry on" is how a traversal gets through.
func TestSafeSegment(t *testing.T) {
	ok := []string{
		"ses_5c663aaed312553f",
		"124546-screenshot.png",
		"a", "A1_-.b", "1-shot.png",
	}
	for _, s := range ok {
		if !safeSegment.MatchString(s) {
			t.Errorf("%q should be allowed", s)
		}
	}

	bad := []string{
		"", " ", "..", "../etc", "..\\windows",
		"a/b", `a\b`, "C:", "C:/Windows/System32/config/SAM",
		"file name.png",      // a space is not in the alphabet
		"sess\x00", "%2e%2e", // encoded traversal, NUL
		"~", "$HOME", "a;b", "a|b", "a*b", "a?b",
	}
	for _, s := range bad {
		if safeSegment.MatchString(s) {
			t.Errorf("%q must be refused", s)
		}
	}
	// A name that is nothing but dots is the traversal, and the pattern refuses
	// it by construction rather than by a check somebody has to remember.
	for _, s := range []string{".", "..", "...", ".hidden"} {
		if safeSegment.MatchString(s) {
			t.Errorf("%q must be refused by the pattern itself", s)
		}
	}
}

// Recognising the app's own paste paths inside a message.
func TestPasteRef(t *testing.T) {
	hits := []struct{ in, sess, name string }{
		{
			`C:/Users/TITO/.goaiteam/pastes/ses_abc/124546-screenshot.png`,
			"ses_abc", "124546-screenshot.png",
		},
		{
			`C:\Users\TITO\.goaiteam\pastes\ses_abc\124546-shot.jpg`,
			"ses_abc", "124546-shot.jpg",
		},
		{
			"look at this\nC:/Users/x/.goaiteam/pastes/ses_9/1-a.webp\nwhat is wrong?",
			"ses_9", "1-a.webp",
		},
	}
	for _, h := range hits {
		m := pasteRef.FindStringSubmatch(h.in)
		if m == nil {
			t.Errorf("no match in %q", h.in)
			continue
		}
		if m[1] != h.sess || m[2] != h.name {
			t.Errorf("%q -> session %q name %q, want %q / %q", h.in, m[1], m[2], h.sess, h.name)
		}
	}

	misses := []string{
		"just a message",
		"C:/Users/x/Documents/photo.png",              // not in the pastes tree
		"C:/Users/x/.goaiteam/pastes/ses_9/notes.txt", // not an image
		"/etc/passwd",
	}
	for _, s := range misses {
		if pasteRef.MatchString(s) {
			t.Errorf("%q should not be treated as a pasted image", s)
		}
	}
}

// A message that is a path becomes a picture, and whatever was typed around it
// stays as the message.
func TestWithImageBlocks(t *testing.T) {
	s := &Server{}
	in := []claudefs.Message{{
		Role: "user",
		Blocks: []claudefs.Block{{
			Kind: claudefs.BlockText,
			Text: "C:/Users/TITO/.goaiteam/pastes/ses_abc/1-shot.png\nwhat is wrong here?",
		}},
	}}
	out := s.withImageBlocks(in)
	if len(out) != 1 {
		t.Fatalf("got %d messages", len(out))
	}
	var text, img int
	var url string
	for _, b := range out[0].Blocks {
		switch b.Kind {
		case claudefs.BlockText:
			text++
			if strings.Contains(b.Text, "pastes") {
				t.Errorf("the path is still in the text: %q", b.Text)
			}
			if !strings.Contains(b.Text, "what is wrong here?") {
				t.Errorf("the typed message was lost: %q", b.Text)
			}
		case claudefs.BlockImage:
			img++
			url = b.URL
			if b.Name != "1-shot.png" {
				t.Errorf("name = %q", b.Name)
			}
		}
	}
	if text != 1 || img != 1 {
		t.Errorf("blocks: %d text, %d image; want 1 and 1", text, img)
	}
	if url != "/api/pastes/ses_abc/1-shot.png" {
		t.Errorf("url = %q", url)
	}

	// A message with no paste path must come back untouched.
	plain := []claudefs.Message{{
		Role:   "assistant",
		Blocks: []claudefs.Block{{Kind: claudefs.BlockText, Text: "no images here"}},
	}}
	got := s.withImageBlocks(plain)
	if len(got[0].Blocks) != 1 || got[0].Blocks[0].Kind != claudefs.BlockText {
		t.Errorf("an ordinary message was rewritten: %+v", got[0].Blocks)
	}
}

// An image sent on its own is a complete message; it must not produce an empty
// text block above the picture.
func TestWithImageBlocksImageOnly(t *testing.T) {
	s := &Server{}
	out := s.withImageBlocks([]claudefs.Message{{
		Role: "user",
		Blocks: []claudefs.Block{{
			Kind: claudefs.BlockText,
			Text: "C:/Users/TITO/.goaiteam/pastes/ses_abc/1-shot.png",
		}},
	}})
	if len(out[0].Blocks) != 1 {
		t.Fatalf("got %d blocks, want just the image: %+v", len(out[0].Blocks), out[0].Blocks)
	}
	if out[0].Blocks[0].Kind != claudefs.BlockImage {
		t.Errorf("block kind = %q", out[0].Blocks[0].Kind)
	}
}

// Claude Code attaches a file it finds referenced in a prompt, and records that
// as a second user entry whose whole text is "[Image: source: <path>]". Once the
// path becomes a picture, that label is machinery the person never wrote.
func TestWithImageBlocksDropsCLIScaffolding(t *testing.T) {
	s := &Server{}
	out := s.withImageBlocks([]claudefs.Message{{
		Role: "user",
		Blocks: []claudefs.Block{{
			Kind: claudefs.BlockText,
			Text: "[Image: source: C:/Users/TITO/.goaiteam/pastes/ses_abc/1-shot.png]",
		}},
	}})
	if len(out[0].Blocks) != 1 || out[0].Blocks[0].Kind != claudefs.BlockImage {
		for _, b := range out[0].Blocks {
			t.Logf("block %q %q", b.Kind, b.Text)
		}
		t.Fatalf("got %d blocks, want just the picture", len(out[0].Blocks))
	}

	// Real words around the path must survive.
	out = s.withImageBlocks([]claudefs.Message{{
		Role: "user",
		Blocks: []claudefs.Block{{
			Kind: claudefs.BlockText,
			Text: "C:/Users/TITO/.goaiteam/pastes/ses_abc/1-shot.png\nwhy is this broken?",
		}},
	}})
	var kept string
	for _, b := range out[0].Blocks {
		if b.Kind == claudefs.BlockText {
			kept = b.Text
		}
	}
	if kept != "why is this broken?" {
		t.Errorf("kept text = %q", kept)
	}
}
