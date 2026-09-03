// Package secrets stores API keys and passwords that agents can use but never
// read.
//
// The rule that shapes this package: there is no code path that returns a secret
// value to an agent, to the HTTP API, or over MCP. An agent can list the names
// and ask for one to be injected into a command's environment; the value is
// resolved in this process, at launch, and goes straight into the child's
// environment. Nothing round-trips through a model's context, where it would end
// up in a transcript on disk.
//
// Encryption is delegated to the operating system — DPAPI on Windows, the
// keychain-derived key elsewhere — so the vault cannot be read by copying the
// file to another machine or another user account. A machine with no usable
// crypto provider gets a refusal rather than a plaintext file.
package secrets

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// ErrNotFound means no secret by that name.
var ErrNotFound = errors.New("no such secret")

// ErrNoCrypto means the platform could not give us a way to encrypt at rest.
var ErrNoCrypto = errors.New("this machine has no usable encryption provider, so the vault is refused rather than written in clear text")

// Vault is the encrypted store.
type Vault struct {
	mu   sync.RWMutex
	path string
	// values is the decrypted map, held in memory only while the process runs.
	values map[string]string
	loaded bool
}

// Open prepares a vault at root/vault.bin.
func Open(root string) (*Vault, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	return &Vault{
		path:   filepath.Join(root, "vault.bin"),
		values: map[string]string{},
	}, nil
}

// Available reports whether encryption works on this machine, without writing
// anything. The UI uses it to explain up front rather than failing on save.
func Available() bool {
	probe := []byte("go-ai-team-probe")
	sealed, err := seal(probe)
	if err != nil {
		return false
	}
	open, err := unseal(sealed)
	return err == nil && string(open) == string(probe)
}

// load reads and decrypts the vault, once.
func (v *Vault) load() error {
	if v.loaded {
		return nil
	}
	b, err := os.ReadFile(v.path)
	if err != nil {
		if os.IsNotExist(err) {
			v.loaded = true
			return nil
		}
		return err
	}
	if len(b) == 0 {
		v.loaded = true
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
	if err != nil {
		return fmt.Errorf("the vault file is corrupt: %w", err)
	}
	plain, err := unseal(raw)
	if err != nil {
		return fmt.Errorf("the vault could not be decrypted on this machine or user account: %w", err)
	}
	m := map[string]string{}
	if err := json.Unmarshal(plain, &m); err != nil {
		return fmt.Errorf("the decrypted vault is not valid: %w", err)
	}
	v.values = m
	v.loaded = true
	return nil
}

// save encrypts and writes the vault atomically, owner-only.
func (v *Vault) save() error {
	plain, err := json.Marshal(v.values)
	if err != nil {
		return err
	}
	sealed, err := seal(plain)
	if err != nil {
		return ErrNoCrypto
	}
	enc := base64.StdEncoding.EncodeToString(sealed)
	tmp := v.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(enc), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, v.path)
}

// Names lists the stored secret names. This is the most an agent ever sees.
func (v *Vault) Names() ([]string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.load(); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(v.values))
	for k := range v.values {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

// Has reports whether a name exists, without revealing anything else.
func (v *Vault) Has(name string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.load(); err != nil {
		return false
	}
	_, ok := v.values[name]
	return ok
}

// Set stores or replaces a value.
func (v *Vault) Set(name, value string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("a secret needs a name")
	}
	if strings.ContainsAny(name, "\n\r\x00") {
		return fmt.Errorf("a secret name cannot contain newlines")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.load(); err != nil {
		return err
	}
	v.values[name] = value
	return v.save()
}

// Delete removes a value.
func (v *Vault) Delete(name string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.load(); err != nil {
		return err
	}
	if _, ok := v.values[name]; !ok {
		return ErrNotFound
	}
	delete(v.values, name)
	return v.save()
}

// Resolve returns a value for injection into a child process environment.
//
// This is intentionally the only reader, and it is unexported from the HTTP
// layer's point of view: the server never calls it in a request handler that
// returns a body. It is called at launch, in the main process, and the value
// goes into an env slice and nowhere else.
func (v *Vault) Resolve(name string) (string, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if err := v.load(); err != nil {
		return "", err
	}
	val, ok := v.values[name]
	if !ok {
		return "", ErrNotFound
	}
	return val, nil
}

// ExpandEnv resolves {{secret:NAME}} references in an environment map. An
// unknown name is an error rather than an empty string, because silently
// launching a command with a blank credential produces a confusing failure far
// from its cause.
func (v *Vault) ExpandEnv(in map[string]string) (map[string]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(in))
	for k, val := range in {
		expanded, err := v.ExpandString(val)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		out[k] = expanded
	}
	return out, nil
}

// ExpandString resolves every {{secret:NAME}} reference in one string.
func (v *Vault) ExpandString(s string) (string, error) {
	const open, close = "{{secret:", "}}"
	if !strings.Contains(s, open) {
		return s, nil
	}
	var b strings.Builder
	rest := s
	for {
		i := strings.Index(rest, open)
		if i < 0 {
			b.WriteString(rest)
			break
		}
		b.WriteString(rest[:i])
		rest = rest[i+len(open):]
		j := strings.Index(rest, close)
		if j < 0 {
			// Unterminated reference: leave it as written rather than guessing.
			b.WriteString(open)
			b.WriteString(rest)
			break
		}
		name := strings.TrimSpace(rest[:j])
		rest = rest[j+len(close):]
		val, err := v.Resolve(name)
		if err != nil {
			return "", fmt.Errorf("secret %q is referenced but not in the vault", name)
		}
		b.WriteString(val)
	}
	return b.String(), nil
}
