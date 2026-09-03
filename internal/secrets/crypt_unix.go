//go:build !windows

package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Off Windows there is no single API equivalent to DPAPI, so the vault is
// AES-256-GCM encrypted under a key file only the user can read.
//
// This is weaker than a real keychain, and worth saying plainly: a process
// running as the same user can read the key. It still means the vault cannot be
// read by copying it to another machine or another account. The alternative —
// clear text — is not acceptable, and shelling out to `security` or
// `secret-tool` fails on exactly the headless machines where this app is most
// useful.

func keyPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".goaiteam", "vault.key"), nil
}

// loadKey reads the key, creating it on first use with owner-only permissions.
func loadKey() ([]byte, error) {
	p, err := keyPath()
	if err != nil {
		return nil, err
	}
	if b, err := os.ReadFile(p); err == nil && len(b) == 32 {
		return b, nil
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(p, key, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

func aead() (cipher.AEAD, error) {
	key, err := loadKey()
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(append(key, []byte("go-ai-team.vault.v1")...))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func seal(plain []byte) ([]byte, error) {
	g, err := aead()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return g.Seal(nonce, nonce, plain, nil), nil
}

func unseal(sealed []byte) ([]byte, error) {
	g, err := aead()
	if err != nil {
		return nil, err
	}
	if len(sealed) < g.NonceSize() {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, body := sealed[:g.NonceSize()], sealed[g.NonceSize():]
	return g.Open(nil, nonce, body, nil)
}
