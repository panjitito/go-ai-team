package secrets

import (
	"os"
	"strings"
	"testing"
)

// assertNotPlaintext fails if a secret value is readable in the file on disk.
// This is the property the whole package exists for, so it is asserted rather
// than assumed.
func assertNotPlaintext(t *testing.T, path, secret string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("could not read the vault file: %v", err)
	}
	if strings.Contains(string(b), secret) {
		t.Fatalf("the secret %q appears in clear text in %s", secret, path)
	}
}
