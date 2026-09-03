// Package sshx manages saved remote servers: running an agent on one, and
// tunnelling a database through one.
//
// Credentials never live in the connection record. A host stores its address,
// its user and either a key path or a reference to a vault entry; the password
// or passphrase is resolved in this process at connect time. That is what makes
// it safe to show the connection list to an agent.
package sshx

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/uniair/go-ai-team/internal/store"
)

// Manager opens connections to saved hosts.
type Manager struct {
	st      *store.Store
	resolve func(secretRef string) (string, error)

	mu sync.Mutex
	// known caches host keys we have accepted, keyed by address.
	known map[string]string
}

// New builds a Manager.
func New(st *store.Store, resolve func(string) (string, error)) *Manager {
	return &Manager{st: st, resolve: resolve, known: map[string]string{}}
}

// clientConfig builds an ssh config for a saved host.
func (m *Manager) clientConfig(h *store.SSHHost) (*ssh.ClientConfig, error) {
	var auths []ssh.AuthMethod

	if h.KeyPath != "" {
		key, err := os.ReadFile(expand(h.KeyPath))
		if err != nil {
			return nil, fmt.Errorf("could not read the key %s: %w", h.KeyPath, err)
		}
		var signer ssh.Signer
		if h.SecretRef != "" && m.resolve != nil {
			// An encrypted key needs its passphrase, which lives in the vault.
			pass, rerr := m.resolve(h.SecretRef)
			if rerr == nil {
				signer, err = ssh.ParsePrivateKeyWithPassphrase(key, []byte(pass))
			} else {
				signer, err = ssh.ParsePrivateKey(key)
			}
		} else {
			signer, err = ssh.ParsePrivateKey(key)
		}
		if err != nil {
			return nil, fmt.Errorf("could not use the key %s: %w", h.KeyPath, err)
		}
		auths = append(auths, ssh.PublicKeys(signer))
	}

	if h.SecretRef != "" && h.KeyPath == "" {
		if m.resolve == nil {
			return nil, fmt.Errorf("this host needs a password from the vault, which is unavailable")
		}
		pass, err := m.resolve(h.SecretRef)
		if err != nil {
			return nil, fmt.Errorf("could not read the secret %q: %w", h.SecretRef, err)
		}
		auths = append(auths, ssh.Password(pass))
	}

	if len(auths) == 0 {
		return nil, fmt.Errorf("host %q has neither a key nor a stored password", h.Name)
	}

	return &ssh.ClientConfig{
		User:            h.User,
		Auth:            auths,
		HostKeyCallback: m.hostKeyCallback(),
		Timeout:         15 * time.Second,
	}, nil
}

// hostKeyCallback pins a host key on first sight and refuses a change after
// that.
//
// This is trust-on-first-use rather than a known_hosts check. It is a deliberate
// trade-off and worth being explicit about: it does not protect the very first
// connection, but it does detect a key changing underneath you afterwards,
// which is the case that actually indicates something is wrong. Accepting any
// key silently — the usual shortcut — would give no protection at all.
func (m *Manager) hostKeyCallback() ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		fp := ssh.FingerprintSHA256(key)
		m.mu.Lock()
		defer m.mu.Unlock()
		if seen, ok := m.known[hostname]; ok {
			if seen != fp {
				return fmt.Errorf("the host key for %s has changed (was %s, now %s); "+
					"refusing to connect until you confirm this is expected", hostname, seen, fp)
			}
			return nil
		}
		m.known[hostname] = fp
		return nil
	}
}

// Dial opens a connection to a saved host.
func (m *Manager) Dial(ctx context.Context, hostID string) (*ssh.Client, error) {
	h, err := m.st.SSHHost(hostID)
	if err != nil {
		return nil, fmt.Errorf("no saved host %s", hostID)
	}
	cfg, err := m.clientConfig(h)
	if err != nil {
		return nil, err
	}
	port := h.Port
	if port == 0 {
		port = 22
	}
	addr := net.JoinHostPort(h.Host, strconv.Itoa(port))

	d := net.Dialer{Timeout: cfg.Timeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("could not reach %s: %w", addr, err)
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ssh handshake with %s failed: %w", addr, err)
	}
	return ssh.NewClient(c, chans, reqs), nil
}

// Test connects, runs a trivial command and reports what it found. Used by the
// UI so a host can be verified when it is saved rather than when it is first
// needed.
func (m *Manager) Test(ctx context.Context, hostID string) (string, error) {
	client, err := m.Dial(ctx, hostID)
	if err != nil {
		return "", err
	}
	defer client.Close()

	sess, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()

	out, err := sess.CombinedOutput("uname -a 2>/dev/null || ver")
	if err != nil && len(out) == 0 {
		return "", fmt.Errorf("connected, but could not run a command: %w", err)
	}
	return string(out), nil
}

// Tunnel forwards a local port to a remote address through a saved host.
//
// The listener binds to 127.0.0.1 only. Binding to all interfaces would quietly
// republish a private database to the whole local network, which is the exact
// opposite of what a tunnel is for.
func (m *Manager) Tunnel(ctx context.Context, hostID, remoteHost string, remotePort int) (string, func(), error) {
	client, err := m.Dial(ctx, hostID)
	if err != nil {
		return "", nil, err
	}

	// Port 0 asks the OS for a free port, so two tunnels never collide.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = client.Close()
		return "", nil, fmt.Errorf("could not reserve a local port: %w", err)
	}

	target := net.JoinHostPort(remoteHost, strconv.Itoa(remotePort))
	done := make(chan struct{})

	go func() {
		for {
			local, err := ln.Accept()
			if err != nil {
				select {
				case <-done:
				default:
				}
				return
			}
			go func() {
				defer local.Close()
				remote, err := client.Dial("tcp", target)
				if err != nil {
					return
				}
				defer remote.Close()
				// Copy both ways; either side closing ends the pair.
				errc := make(chan error, 2)
				go func() { _, e := io.Copy(remote, local); errc <- e }()
				go func() { _, e := io.Copy(local, remote); errc <- e }()
				<-errc
			}()
		}
	}()

	closeFn := func() {
		close(done)
		_ = ln.Close()
		_ = client.Close()
	}

	// Wait until the listener really accepts a connection before handing the
	// address back, so a caller cannot dial a port that is not ready yet.
	addr := ln.Addr().String()
	if err := waitReady(addr, 5*time.Second); err != nil {
		closeFn()
		return "", nil, err
	}
	return addr, closeFn, nil
}

// waitReady polls a local address until it accepts a TCP connection.
func waitReady(addr string, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("the local tunnel port never became ready")
}

// SSHCommand builds the argv for an interactive ssh session to a saved host, so
// an agent terminal can run on the remote machine using the user's own ssh
// client rather than a reimplementation of it.
//
// Using the real client matters: it picks up their ~/.ssh/config, their agent,
// their jump hosts and their known_hosts, none of which we would inherit by
// dialling ourselves.
func (m *Manager) SSHCommand(hostID string, remoteCmd string) ([]string, error) {
	h, err := m.st.SSHHost(hostID)
	if err != nil {
		return nil, fmt.Errorf("no saved host %s", hostID)
	}
	args := []string{"ssh", "-tt"}
	if h.Port != 0 && h.Port != 22 {
		args = append(args, "-p", strconv.Itoa(h.Port))
	}
	if h.KeyPath != "" {
		args = append(args, "-i", expand(h.KeyPath))
	}
	dest := h.Host
	if h.User != "" {
		dest = h.User + "@" + h.Host
	}
	args = append(args, dest)
	if remoteCmd != "" {
		args = append(args, remoteCmd)
	}
	return args, nil
}

// expand resolves a leading ~ in a path.
func expand(p string) string {
	if len(p) > 0 && p[0] == '~' {
		if home, err := os.UserHomeDir(); err == nil {
			rest := p[1:]
			for len(rest) > 0 && (rest[0] == '/' || rest[0] == '\\') {
				rest = rest[1:]
			}
			if rest == "" {
				return home
			}
			return home + string(os.PathSeparator) + rest
		}
	}
	return p
}
