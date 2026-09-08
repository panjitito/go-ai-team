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
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/panjitito/go-ai-team/internal/accounts"
	"github.com/panjitito/go-ai-team/internal/ai"
	"github.com/panjitito/go-ai-team/internal/automation"
	"github.com/panjitito/go-ai-team/internal/browser"
	"github.com/panjitito/go-ai-team/internal/catalog"
	"github.com/panjitito/go-ai-team/internal/claudefs"
	"github.com/panjitito/go-ai-team/internal/dbx"
	"github.com/panjitito/go-ai-team/internal/desktop"
	"github.com/panjitito/go-ai-team/internal/secrets"
	"github.com/panjitito/go-ai-team/internal/server"
	"github.com/panjitito/go-ai-team/internal/session"
	"github.com/panjitito/go-ai-team/internal/sshx"
	"github.com/panjitito/go-ai-team/internal/store"
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
	// `go-ai-team statusline` is the status-line command the app can install
	// into an account. It reads one JSON payload on stdin and prints one line,
	// so it has to be a subcommand and it has to stay silent about failure:
	// see statusline_cmd.go.
	if len(os.Args) > 1 && os.Args[1] == "statusline" {
		runStatusLine()
		return
	}

	var (
		port   = flag.Int("port", 0, "port to listen on (default: saved setting, else 7777)")
		host   = flag.String("host", "127.0.0.1", "address to bind; use 0.0.0.0 to reach it from your phone")
		open   = flag.Bool("open", true, "open the UI on start")
		browse = flag.String("browser", "desktop",
			"how to open it: desktop (a real app window, no browser), app (own Chrome profile), tab, system (your normal browser), none")
		profile = flag.String("browser-profile", "",
			"where the app's Chrome profile lives (default: <state>/browser)")
		shortcut = flag.Bool("install-shortcut", false,
			"create a desktop launcher for the app, then exit")
		showVer = flag.Bool("version", false, "print the version and exit")
	)
	flag.Parse()

	mode, err := browser.ParseMode(*browse)
	if err != nil {
		log.Fatal(err)
	}
	if !*open {
		mode = browser.ModeNone
	}

	if *showVer {
		fmt.Printf("go-ai-team %s (%s/%s, %s)\n", version, runtime.GOOS, runtime.GOARCH, runtime.Version())
		return
	}

	// Creating a desktop launcher writes outside our own state directory, so it
	// only ever happens when explicitly asked for.
	if *shortcut {
		exe, err := os.Executable()
		if err != nil {
			log.Fatalf("cannot locate this executable: %v", err)
		}
		sc, err := browser.InstallShortcut(exe, *port)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Created %s\n  %s\n", sc.Path, sc.Note)
		return
	}

	app, err := build(*host)
	if err != nil {
		log.Fatal(err)
	}

	profileDir := *profile
	if profileDir == "" {
		profileDir = filepath.Join(app.st.RootDir(), "browser")
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

	// A native window is preferred and falls back rather than failing: a machine
	// without the WebView2 runtime should still get its UI, just in a browser.
	if mode == browser.ModeDesktop {
		if ok, _ := desktop.Available(); !ok {
			mode = browser.ModeApp
		}
	}

	// A desktop app does not leave a console box sitting behind its window, so
	// the console is given back — see internal/desktop/console_windows.go for
	// why hiding it stopped working on Windows 11.
	//
	// Here rather than further down, next to the window: after this, output goes
	// to a file, and reassigning os.Stdout underneath a goroutine that is
	// already writing to it is a race. Nothing has started yet at this point.
	//
	// Two conditions, and both are about not stranding anybody.
	//
	// There has to be a UI, because it now carries the Quit that Ctrl-C used to
	// be — `--browser none` is somebody deliberately running this as a bare
	// server from a terminal, and taking their terminal away would leave them
	// with no way to stop it at all.
	//
	// And it has to be loopback. Bound to the network the banner is showing an
	// access token that exists nowhere else, and taking the window away would
	// take the token with it. The token is deliberately not written to the log.
	released := false
	if mode != browser.ModeNone && app.loopback {
		released = desktop.ReleaseOwnConsole(filepath.Join(app.st.RootDir(), "log", "app.log"))
	}

	// After the console has gone, not before. Printed first, the banner went to a
	// window that was about to be destroyed and nobody ever read it; printed here
	// it lands in the log, which is where somebody looks when the app started and
	// they cannot see it. When there is still a console it goes there, as always.
	// The server has to know its own address and how the app opened its first
	// window, so a pop-out can be opened the same way on an address the server
	// knows rather than one a request claimed in a header.
	app.srv.SetWindowing(localURL, mode)

	stopWith := "Ctrl-C to stop."
	switch {
	case released && mode == browser.ModeDesktop:
		stopWith = "Close the window, or Quit from the notification area."
	case released:
		stopWith = "Quit from Settings in the app, or stop the process."
	}
	printBanner(banner{
		Port: listenPort, URL: localURL, Token: app.token,
		Loopback: app.loopback, Home: app.st.RootDir(), VaultOK: app.vault != nil,
		StopWith: stopWith, Plain: released,
	})

	if mode != browser.ModeNone && mode != browser.ModeDesktop {
		go func() {
			// A moment for the listener to be serving, so the first request is
			// not a connection refused that the user sees as a blank window.
			time.Sleep(250 * time.Millisecond)
			what, err := browser.Open(browser.Opts{
				URL: localURL, ProfileDir: profileDir, Mode: mode,
			})
			if err != nil {
				log.Printf("could not open a browser (%v); visit %s", err, localURL)
				return
			}
			if what != "" {
				fmt.Printf("  %s\n", what)
			}
		}()
	}

	go func() {
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server stopped: %v", err)
		}
	}()

	shutdown := func(why string) {
		fmt.Printf("\n%s…\n", why)
		// Extra windows first. One left behind is a view onto a server that has
		// stopped answering, which reads as the app hanging rather than as the
		// app having gone.
		desktop.CloseWindows()
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

	// One way out, whoever asks for it.
	//
	// Ctrl-C was the only one until the console started being given back, and a
	// console that is not there cannot deliver a Ctrl-C. The window's close
	// button, Quit on the notification icon and Quit in the page all arrive
	// here, and they arrive once: two clicks must not start two shutdowns.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)

	stop := make(chan struct{})
	var stopOnce sync.Once
	askStop := func() { stopOnce.Do(func() { close(stop) }) }
	go func() {
		<-sig
		askStop()
	}()
	app.srv.OnQuit(askStop)

	if mode == browser.ModeDesktop {
		// The window owns the main thread from here: Windows requires the
		// message loop to run on the thread that created the window, and main
		// is the only thread we can guarantee that for. Ctrl-C still works —
		// closing the window from the signal handler ends the loop, and control
		// returns here either way.
		if err := runDesktop(app, localURL, stop); err != nil {
			log.Printf("could not open the app window (%v); visit %s", err, localURL)
			<-stop
		}
		shutdown("shutting down")
		return
	}

	<-stop
	shutdown("shutting down")
}

// runDesktop shows the app window and returns when it closes.
func runDesktop(app *app, url string, stop <-chan struct{}) error {
	state := app.st.RootDir()
	desktop.EnsureIcon(state)

	var start desktop.Bounds
	if b := app.st.Settings().Window; b != nil {
		start = desktop.Bounds{X: b.X, Y: b.Y, W: b.W, H: b.H, Maximized: b.Maximized}
	}

	done := make(chan struct{})
	defer close(done)

	// Flash the taskbar button when an agent stops to ask something.
	//
	// This is the payoff for running several at once: they do not finish
	// together, and the one that has stopped for an answer is invisible until
	// you happen to look at it. Deliberately a flash and not a focus grab — an
	// agent's question is not a reason to yank the cursor out of whatever is
	// being typed somewhere else.
	go watchForQuestions(app.sm, done)

	closer := make(chan func(), 1)
	go func() {
		// Wait for the window to exist before there is anything to close. A stop
		// asked for during startup is not lost: the channel stays closed, so it
		// is still readable on the next line.
		var closeWindow func()
		select {
		case closeWindow = <-closer:
		case <-done:
			return
		}
		// A stop from anywhere else — Ctrl-C where there is still a console, or
		// Quit in the page — has to take the window down with it, rather than
		// leave it on screen after the process has gone.
		select {
		case <-stop:
			closeWindow()
		case <-done:
		}
	}()

	return desktop.Run(desktop.Opts{
		URL:     url,
		Title:   "Go AI Team",
		DataDir: filepath.Join(state, "window"),
		Start:   start,
		Ready:   func(stop func()) { closer <- stop },
		OnClose: func(b desktop.Bounds) {
			if !b.Valid() {
				return
			}
			_, _ = app.st.UpdateSettings(func(s *store.Settings) {
				s.Window = &store.WindowBounds{X: b.X, Y: b.Y, W: b.W, H: b.H, Maximized: b.Maximized}
			})
		},
	})
}

// watchForQuestions asks the window for attention while any agent is waiting on
// a person, and clears it once none are.
//
// It tracks the set rather than counting events, because the two are not
// symmetric: a session that exits while its question is on screen never sends
// the matching "answered", and a counter would sit at one for ever with nothing
// to clear it.
func watchForQuestions(sm *session.Manager, done <-chan struct{}) {
	events, unsubscribe := sm.Subscribe()
	defer unsubscribe()

	waiting := map[string]bool{}
	for {
		select {
		case <-done:
			return
		case e, ok := <-events:
			if !ok {
				return
			}
			switch e.Type {
			case "session.needs-you":
				if !waiting[e.SessionID] {
					waiting[e.SessionID] = true
					desktop.Attention()
					// The taskbar flash lands nowhere while the window is in
					// the notification area, so the icon carries the count and
					// raises a balloon of its own.
					desktop.SetWaiting(len(waiting))
				}
			case "session.answered", "session.exited", "session.removed":
				if _, was := waiting[e.SessionID]; !was {
					continue
				}
				delete(waiting, e.SessionID)
				desktop.SetWaiting(len(waiting))
				if len(waiting) == 0 {
					desktop.StopAttention()
				}
			}
		}
	}
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

// banner is the startup summary and where it is going.
type banner struct {
	Port     int
	URL      string
	Token    string
	Loopback bool
	Home     string
	VaultOK  bool
	// StopWith is how to stop the app, which is not always Ctrl-C: with the
	// console released there is no Ctrl-C to press, and saying so in a log
	// somebody is reading precisely because they cannot find the app would be a
	// poor joke.
	StopWith string
	// Plain drops the colour and the box drawing. The banner goes to a log file
	// when there is no console left to print it to, and escape sequences and
	// box-drawing characters in a file that Notepad and `type` will open are
	// noise at best and mojibake at worst.
	Plain bool
}

func printBanner(b banner) {
	rule, orange, yellow, off := "─", "\x1b[38;5;208m", "\x1b[33m", "\x1b[0m"
	if b.Plain {
		rule, orange, yellow, off = "-", "", "", ""
	}
	line := strings.Repeat(rule, 58)

	fmt.Printf("\n%s  Go AI Team%s %s\n  %s\n", orange, off, version, line)
	fmt.Printf("  Desktop   %s\n", b.URL)

	if b.Loopback {
		if ips := server.LocalIPs(); len(ips) > 0 {
			fmt.Printf("  Phone     bound to loopback only.\n")
			fmt.Printf("            restart with --host 0.0.0.0 to reach it at\n")
			fmt.Printf("            http://%s:%d\n", ips[0], b.Port)
		}
	} else {
		fmt.Printf("\n  Reachable on your network. The API needs this token:\n")
		for _, ip := range server.LocalIPs() {
			fmt.Printf("  Phone     http://%s:%d/?token=%s\n", ip, b.Port, b.Token)
		}
		fmt.Printf("\n  %sAnyone on this network with that link can drive your\n", yellow)
		fmt.Printf("  terminals. The token is new on every start.%s\n", off)
	}

	fmt.Printf("\n  State     %s\n", b.Home)
	if !b.VaultOK {
		fmt.Printf("  %sVault     disabled: no encryption provider on this machine%s\n", yellow, off)
	}
	fmt.Printf("  %s\n  %s\n\n", line, b.StopWith)
}
