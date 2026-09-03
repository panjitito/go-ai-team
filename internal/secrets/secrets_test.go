package secrets

import (
	"path/filepath"
	"testing"
)

func TestSealUnsealRoundTrip(t *testing.T) {
	if !Available() {
		t.Skip("no encryption provider on this machine")
	}
	plain := []byte(`{"STRIPE_SECRET_KEY":"sk_live_example","EMPTY":""}`)
	sealed, err := seal(plain)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if string(sealed) == string(plain) {
		t.Fatal("sealed output equals the plaintext; nothing was encrypted")
	}
	open, err := unseal(sealed)
	if err != nil {
		t.Fatalf("unseal: %v", err)
	}
	if string(open) != string(plain) {
		t.Fatalf("round trip changed the value:\n got %q\nwant %q", open, plain)
	}
}

func TestVaultSetResolveDelete(t *testing.T) {
	if !Available() {
		t.Skip("no encryption provider on this machine")
	}
	dir := t.TempDir()
	v, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Set("TOKEN", "abc123"); err != nil {
		t.Fatal(err)
	}
	if got, err := v.Resolve("TOKEN"); err != nil || got != "abc123" {
		t.Fatalf("Resolve = %q, %v", got, err)
	}
	if !v.Has("TOKEN") {
		t.Error("Has should be true")
	}
	names, err := v.Names()
	if err != nil || len(names) != 1 || names[0] != "TOKEN" {
		t.Fatalf("Names = %v, %v", names, err)
	}

	// The file on disk must not contain the value in readable form.
	assertNotPlaintext(t, filepath.Join(dir, "vault.bin"), "abc123")

	// A fresh Vault over the same directory must decrypt what the first wrote.
	v2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := v2.Resolve("TOKEN"); got != "abc123" {
		t.Errorf("reopened vault lost the value, got %q", got)
	}

	if err := v.Delete("TOKEN"); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Resolve("TOKEN"); err == nil {
		t.Error("Resolve should fail after Delete")
	}
	if err := v.Delete("TOKEN"); err == nil {
		t.Error("deleting twice should report not-found")
	}
}

func TestExpandString(t *testing.T) {
	if !Available() {
		t.Skip("no encryption provider on this machine")
	}
	v, _ := Open(t.TempDir())
	if err := v.Set("PW", "hunter2"); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"plain", "plain", false},
		{"{{secret:PW}}", "hunter2", false},
		{"pre-{{secret:PW}}-post", "pre-hunter2-post", false},
		{"{{secret:PW}}{{secret:PW}}", "hunter2hunter2", false},
		{"{{secret: PW }}", "hunter2", false},
		// A missing name must fail loudly. Substituting an empty string would
		// launch a command with a blank credential and fail far from the cause.
		{"{{secret:MISSING}}", "", true},
		// An unterminated reference is left as written rather than swallowed.
		{"{{secret:PW", "{{secret:PW", false},
	}
	for _, c := range cases {
		got, err := v.ExpandString(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ExpandString(%q) should have failed", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ExpandString(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ExpandString(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestExpandEnv(t *testing.T) {
	if !Available() {
		t.Skip("no encryption provider on this machine")
	}
	v, _ := Open(t.TempDir())
	_ = v.Set("DB_PASS", "s3cret")

	out, err := v.ExpandEnv(map[string]string{
		"DATABASE_URL": "postgres://u:{{secret:DB_PASS}}@localhost/db",
		"PLAIN":        "kept",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["DATABASE_URL"] != "postgres://u:s3cret@localhost/db" {
		t.Errorf("DATABASE_URL = %q", out["DATABASE_URL"])
	}
	if out["PLAIN"] != "kept" {
		t.Errorf("PLAIN = %q", out["PLAIN"])
	}
	if _, err := v.ExpandEnv(map[string]string{"X": "{{secret:NOPE}}"}); err == nil {
		t.Error("a missing secret in ExpandEnv should be an error")
	}
}

func TestSetRejectsBadNames(t *testing.T) {
	if !Available() {
		t.Skip("no encryption provider on this machine")
	}
	v, _ := Open(t.TempDir())
	for _, bad := range []string{"", "   ", "with\nnewline", "with\x00null"} {
		if err := v.Set(bad, "x"); err == nil {
			t.Errorf("Set(%q) should have been rejected", bad)
		}
	}
}
