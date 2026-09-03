// Go AI Team — run every Claude Code account you own, side by side, in one
// window, with a board, a scheduler and an agent-drivable IDE around it.
//
// The whole thing is one binary. It serves a web UI, spawns agent CLIs in real
// pseudo-terminals, and binds each one to an account by setting that provider's
// own config-directory variable before the process starts. There is no proxy in
// front of the API, no credential ever passes through this program, and nothing
// is sent anywhere: every number on screen is read from files the CLI already
// wrote to your disk, and every AI helper runs against the subscription you
// already pay for.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/uniair/go-ai-team/internal/accounts"
	"github.com/uniair/go-ai-team/internal/ai"
	"github.com/uniair/go-ai-team/internal/automation"
	"github.com/uniair/go-ai-team/internal/catalog"
	"github.com/uniair/go-ai-team/internal/claudefs"
	"github.com/uniair/go-ai-team/internal/dbx"
	"github.com/uniair/go-ai-team/internal/secrets"
	"github.com/uniair/go-ai-team/internal/server"
	"github.com/uniair/go-ai-team/internal/session"
	"github.com/uniair/go-ai-team/internal/sshx"
	"github.com/uniair/go-ai-team/internal/store"
)

// version is stamped at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	// `go-ai-team mcp` is how an agent CLI talks to the app: it speaks JSON-RPC
	// on stdio, so it must be a subcommand rather than a flag on the server.
	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		if err := runMCP(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "go-ai-team mcp:", err)
			os.Exit(1)
		}
		return
	}

	var (
		port    = flag.Int("port", 0, "port to listen on (default: saved setting, else 7777)")
		host    = flag.String("host", "127.0.0.1", "address to bind; use 0.0.0.0 to reach it from your phone")
		open    = flag.Bool("open", true, "open the UI in your browser on start")
		showVer = flag.Bool("version", false, "print the version and exit")
	)
	flag.Parse()

	if *showVer {
		fmt.Printf("go-ai-team %s (%s/%s, %s)\n", version, runtime.GOOS, runtime.GOARCH, runtime.Version())
		return
	}

	app, err := build(*host)
	if err != nil {
		log.Fatal(err)
	}

	listenPort := app.settings.Port
	if *port != 0 {
		listenPort = *port
	}
	if listenPort == 0 {
		listenPort = 7777
	}

	addr := net.JoinHostPort(*host, server.PortString(listenPort))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("cannot listen on %s: %v\n\nIs Go AI Team already running? Try --port 7778.", addr, err)
	}

	ctx, stopBackground := context.WithCancel(context.Background())
	defer stopBackground()

	// Automation starts only once the port is ours, so two instances cannot
	// both fire the same schedule.
	app.sched.Start(ctx)
	go app.hooks.ReplayQueued()

	httpSrv := &http.Server{
		Handler: app.srv.Handler(),
		// No global write timeout: a terminal websocket is meant to stay open
		// for hours. Read headers are still bounded.
		ReadHeaderTimeout: 10 * time.Second,
	}

	localURL := fmt.Sprintf("http://localhost:%d", listenPort)
	printBanner(listenPort, localURL, app.token, app.loopback, app.st.RootDir(), app.vault != nil)

	if *open {
		go openBrowser(localURL)
	}

	go func() {
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server stopped: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	fmt.Println("\nshutting down…")

	// Record what was running before killing it, so "restore on launch" has
	// something to restore.
	app.srv.SaveRestoreState()
	stopBackground()
	for _, s := range app.sm.Sessions() {
		_ = app.sm.Stop(s.ID)
	}
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutCtx)
	fmt.Println("bye.")
}

// app holds the assembled subsystems.
type app struct {
	st       *store.Store
	settings store.Settings
	sm       *session.Manager
	srv      *server.Server
	sched    *automation.Scheduler
	hooks    *automation.Hooks
	vault    *secrets.Vault
	token    string
	loopback bool
}

// build wires everything together.
//
// The order matters in one place: the session manager is created before the
// things that launch agents, because the scheduler and the webhook engine both
// take it as their Launcher.
func build(host string) (*app, error) {
	st, err := store.Open()
	if err != nil {
		return nil, fmt.Errorf("cannot open the Go AI Team store: %w", err)
	}
	settings := st.Settings()

	accs := accounts.New(st)
	sm := session.NewManager(st, accs)

	// Keep the shared user layer in step on every start, so a skill added to
	// ~/.claude yesterday is visible to every account today.
	if settings.ShareUserLayer {
		if err := accs.SyncUserLayer(); err != nil {
			log.Printf("note: could not sync your CLI configuration into every account: %v", err)
		}
	}

	// The AI helpers resolve their account through the same cascade an agent
	// does, and refuse to run on an account that is only nominally signed in.
	aiRunner := ai.New(st,
		func(id string) (string, string, bool) {
			a, err := st.Account(id)
			if err != nil {
				return "", "", false
			}
			return a.Dir, a.Name, claudefs.SignedIn(a.Dir) && !a.Benched()
		},
		func() (string, string, string, bool) {
			for _, a := range st.AccountsFor(store.ProviderClaude) {
				if claudefs.SignedIn(a.Dir) && !a.Benched() {
					return a.ID, a.Dir, a.Name, true
				}
			}
			// Fall back to the provider's own directory, which is signed in on
			// most machines even when no account has been registered here.
			sys := accounts.SystemDir(store.ProviderClaude)
			if claudefs.SignedIn(sys) {
				return "", sys, "system default", true
			}
			return "", "", "", false
		},
		session.CleanEnv,
	)

	cat := catalog.NewManager(st.RootDir())

	// A machine that cannot encrypt gets no vault rather than a plaintext one.
	var vault *secrets.Vault
	if secrets.Available() {
		v, err := secrets.Open(st.RootDir())
		if err != nil {
			log.Printf("note: the secret vault could not be opened: %v", err)
		} else {
			vault = v
		}
	} else {
		log.Printf("note: no encryption provider on this machine, so the secret vault is disabled")
	}

	resolveSecret := func(name string) (string, error) {
		if vault == nil {
			return "", errors.New("the secret vault is unavailable on this machine")
		}
		return vault.Resolve(name)
	}

	ssh := sshx.New(st, resolveSecret)
	db := dbx.New(st, resolveSecret, ssh.Tunnel)

	sched := automation.NewScheduler(st, sm, sm)
	hooks := automation.NewHooks(st, sm, sm)

	loopback := isLoopbackHost(host)
	token := ""
	if !loopback {
		token = randomToken()
	}

	srv := server.New(server.Deps{
		Store: st, Accounts: accs, Sessions: sm,
		AI: aiRunner, Catalog: cat, Vault: vault, DB: db, SSH: ssh,
		Sched: sched, Hooks: hooks,
		Token: token, Loopback: loopback,
	})

	return &app{
		st: st, settings: settings, sm: sm, srv: srv,
		sched: sched, hooks: hooks, vault: vault,
		token: token, loopback: loopback,
	}, nil
}

func isLoopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func randomToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// A weak token would be worse than refusing to bind publicly.
		log.Fatalf("cannot generate an access token: %v", err)
	}
	return hex.EncodeToString(b[:])
}

func printBanner(port int, localURL, token string, loopback bool, home string, vaultOK bool) {
	line := strings.Repeat("─", 58)
	fmt.Printf("\n\x1b[38;5;208m  Go AI Team\x1b[0m %s\n  %s\n", version, line)
	fmt.Printf("  Desktop   %s\n", localURL)

	if loopback {
		if ips := server.LocalIPs(); len(ips) > 0 {
			fmt.Printf("  Phone     bound to loopback only.\n")
			fmt.Printf("            restart with --host 0.0.0.0 to reach it at\n")
			fmt.Printf("            http://%s:%d\n", ips[0], port)
		}
	} else {
		fmt.Printf("\n  Reachable on your network. The API needs this token:\n")
		for _, ip := range server.LocalIPs() {
			fmt.Printf("  Phone     http://%s:%d/?token=%s\n", ip, port, token)
		}
		fmt.Printf("\n  \x1b[33mAnyone on this network with that link can drive your\n")
		fmt.Printf("  terminals. The token is new on every start.\x1b[0m\n")
	}

	fmt.Printf("\n  State     %s\n", home)
	if !vaultOK {
		fmt.Printf("  \x1b[33mVault     disabled: no encryption provider on this machine\x1b[0m\n")
	}
	fmt.Printf("  %s\n  Ctrl-C to stop.\n\n", line)
}

// openBrowser launches the default browser without blocking startup.
func openBrowser(url string) {
	time.Sleep(300 * time.Millisecond)
	var err error
	switch runtime.GOOS {
	case "windows":
		err = runDetached("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		err = runDetached("open", url)
	default:
		err = runDetached("xdg-open", url)
	}
	if err != nil {
		log.Printf("could not open a browser automatically; visit %s", url)
	}
}
