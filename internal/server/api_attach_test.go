package server

import (
	"strings"
	"testing"
)

// The type is decided by the bytes, never by the request.
//
// The header is whatever the caller chose to say, and it picks the extension a
// file is written under in a directory the agent is allowed to read. Sniffing is
// what keeps "paste an image" from becoming "write any file you like there".
func TestImageKind(t *testing.T) {
	ok := []struct {
		name string
		in   []byte
		kind string
		ext  string
	}{
		{"png", []byte("\x89PNG\r\n\x1a\n....."), "image/png", ".png"},
		{"jpeg", []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0, 0, 0}, "image/jpeg", ".jpg"},
		{"gif87", []byte("GIF87a and then some"), "image/gif", ".gif"},
		{"gif89", []byte("GIF89a and then some"), "image/gif", ".gif"},
		{"webp", []byte("RIFF\x00\x00\x00\x00WEBPVP8 "), "image/webp", ".webp"},
	}
	for _, c := range ok {
		kind, ext, err := imageKind(c.in)
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.name, err)
			continue
		}
		if kind != c.kind || ext != c.ext {
			t.Errorf("%s: got %q %q, want %q %q", c.name, kind, ext, c.kind, c.ext)
		}
	}

	bad := []struct {
		name string
		in   []byte
	}{
		{"empty", nil},
		{"text", []byte("just some text")},
		// The dangerous case: a script wearing an image's name.
		{"script", []byte("#!/bin/sh\nrm -rf /\n")},
		{"html", []byte("<html><script>alert(1)</script>")},
		{"windows exe", []byte("MZ\x90\x00\x03\x00\x00\x00")},
		// Truncated magic must not be read past the end.
		{"short png", []byte("\x89PNG")},
		{"short riff", []byte("RIFF")},
	}
	for _, c := range bad {
		if _, _, err := imageKind(c.in); err == nil {
			t.Errorf("%s: expected a refusal, got none", c.name)
		}
	}
}

// The name comes from a browser, so it is untrusted input used to build a path.
func TestAttachName(t *testing.T) {
	cases := []struct {
		given string
		want  string // the part after the timestamp
	}{
		{"screenshot.png", "screenshot.png"},
		{"", "pasted.png"},
		{"   ", "pasted.png"},
		// Traversal, separators and drive letters must not survive.
		{"../../../etc/passwd", "passwd.png"},
		{`..\..\windows\system32\evil`, "evil.png"},
		{"C:/Windows/System32/cmd", "cmd.png"},
		{"a/b/c/deep.png", "deep.png"},
		// Everything outside a safe alphabet is dropped, not replaced.
		{"my photo (1)!.png", "myphoto1.png"},
		{"...", "pasted.png"},
		{"$(rm -rf ~).png", "rm-rf.png"},
	}
	for _, c := range cases {
		got := attachName(c.given, ".png")
		if !strings.HasSuffix(got, c.want) {
			t.Errorf("attachName(%q) = %q, want it to end with %q", c.given, got, c.want)
		}
		for _, bad := range []string{"..", "/", `\`, ":"} {
			if strings.Contains(got, bad) {
				t.Errorf("attachName(%q) = %q, contains %q", c.given, got, bad)
			}
		}
	}

	// A very long name is truncated rather than refused.
	long := attachName(strings.Repeat("a", 500), ".png")
	if len(long) > 64 {
		t.Errorf("name not truncated: %d chars", len(long))
	}
}
