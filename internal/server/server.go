// Package server exposes the Go AI Team API and serves the web UI.
//
// The whole app is one HTTP server. That is what lets a single binary be both
// the desktop app (open it in a browser) and the mobile app (open the same URL
// from a phone on the same network), with no Electron shell and no second
// codebase to keep in sync.
package server

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/uniair/go-ai-team/internal/accounts"
	"github.com/uniair/go-ai-team/internal/ai"
	"github.com/uniair/go-ai-team/internal/automation"
	"github.com/uniair/go-ai-team/internal/catalog"
	"github.com/uniair/go-ai-team/internal/claudefs"
	"github.com/uniair/go-ai-team/internal/dbx"
	"github.com/uniair/go-ai-team/internal/secrets"
	"github.com/uniair/go-ai-team/internal/session"
	"github.com/uniair/go-ai-team/internal/sshx"
	"github.com/uniair/go-ai-team/internal/store"
)

//go:embed all:web
var webFS embed.FS

// Server wires every subsystem to HTTP.
type Server struct {
	st   *store.Store
	accs *accounts.Manager
	sm   *session.Manager

	// ai runs the app's own helper prompts against the user's signed-in CLI, so
	// commit messages, suggestions and clustering are not metered by us.
	ai *ai.Runner
	// cat holds the built-in roles plus whatever the user imported.
	cat *catalog.Manager
	// vault is nil when the platform cannot encrypt at rest; every caller
	// checks, because writing secrets in clear text is refused rather than
	// silently allowed.
	vault *secrets.Vault
	db    *dbx.Client
	ssh   *sshx.Manager
	sched *automation.Scheduler
	hooks *automation.Hooks

	// token guards the API when the server is reachable beyond loopback. It is
	// generated per run and printed once, so binding to the LAN for phone access
	// does not silently expose an unauthenticated terminal to the network.
	token    string
	loopback bool
}

// Deps is everything a Server needs, assembled by main.
type Deps struct {
	Store    *store.Store
	Accounts *accounts.Manager
	Sessions *session.Manager
	AI       *ai.Runner
	Catalog  *catalog.Manager
	Vault    *secrets.Vault
	DB       *dbx.Client
	SSH      *sshx.Manager
	Sched    *automation.Scheduler
	Hooks    *automation.Hooks
	Token    string
	Loopback bool
}

// New builds a Server.
func New(d Deps) *Server {
	return &Server{
		st: d.Store, accs: d.Accounts, sm: d.Sessions,
		ai: d.AI, cat: d.Catalog, vault: d.Vault, db: d.DB, ssh: d.SSH,
		sched: d.Sched, hooks: d.Hooks,
		token: d.Token, loopback: d.Loopback,
	}
}

// Handler returns the fully routed HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// --- accounts ---
	mux.HandleFunc("GET /api/accounts", s.listAccounts)
	mux.HandleFunc("POST /api/accounts", s.createAccount)
	mux.HandleFunc("PATCH /api/accounts/{id}", s.patchAccount)
	mux.HandleFunc("DELETE /api/accounts/{id}", s.deleteAccount)
	mux.HandleFunc("POST /api/accounts/{id}/login", s.loginAccount)
	mux.HandleFunc("POST /api/accounts/{id}/unbench", s.unbenchAccount)
	mux.HandleFunc("GET /api/accounts/discover", s.discoverAccounts)
	mux.HandleFunc("GET /api/accounts/userlayer", s.getUserLayer)
	mux.HandleFunc("POST /api/accounts/userlayer/sync", s.syncUserLayer)

	// --- folders ---
	mux.HandleFunc("GET /api/folders", s.listFolders)
	mux.HandleFunc("POST /api/folders", s.createFolder)
	mux.HandleFunc("PATCH /api/folders/{id}", s.patchFolder)
	mux.HandleFunc("DELETE /api/folders/{id}", s.deleteFolder)

	// --- projects ---
	mux.HandleFunc("GET /api/projects", s.listProjects)
	mux.HandleFunc("POST /api/projects", s.createProject)
	mux.HandleFunc("PATCH /api/projects/{id}", s.patchProject)
	mux.HandleFunc("DELETE /api/projects/{id}", s.deleteProject)

	// --- agents ---
	mux.HandleFunc("GET /api/agents", s.listAgents)
	mux.HandleFunc("POST /api/agents", s.createAgent)
	mux.HandleFunc("PATCH /api/agents/{id}", s.patchAgent)
	mux.HandleFunc("DELETE /api/agents/{id}", s.deleteAgent)
	mux.HandleFunc("POST /api/agents/{id}/start", s.startAgent)
	mux.HandleFunc("GET /api/agents/{id}/resolve", s.resolveAgent)

	// --- sessions ---
	mux.HandleFunc("GET /api/sessions", s.listSessions)
	mux.HandleFunc("POST /api/sessions/{id}/stop", s.stopSession)
	mux.HandleFunc("DELETE /api/sessions/{id}", s.removeSession)
	mux.HandleFunc("POST /api/sessions/{id}/input", s.sendInput)
	mux.HandleFunc("GET /api/sessions/{id}/usage", s.sessionUsage)
	mux.HandleFunc("GET /api/sessions/{id}/scrollback", s.sessionScrollback)

	// --- settings, misc ---
	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("PATCH /api/settings", s.patchSettings)
	mux.HandleFunc("GET /api/doctor", s.doctor)
	mux.HandleFunc("GET /api/fs/list", s.listDir)

	// --- the rest, grouped by area ---
	s.routeWork(mux)
	s.routeEnv(mux)
	s.routeMisc(mux)

	// --- realtime ---
	mux.HandleFunc("/ws/pty", s.wsPTY)
	mux.HandleFunc("/ws/events", s.wsEvents)

	// --- static UI ---
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("embedded web assets missing: %v", err)
	}
	mux.Handle("/", spaHandler{fs: http.FS(sub)})

	return s.withAuth(s.withCommon(mux))
}

// spaHandler serves the embedded UI, falling back to index.html so a deep link
// still loads the app.
type spaHandler struct{ fs http.FileSystem }

func (h spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/")
	if p == "" {
		p = "index.html"
	}
	f, err := h.fs.Open(p)
	if err != nil {
		f, err = h.fs.Open("index.html")
		if err != nil {
			http.Error(w, "UI not built", http.StatusInternalServerError)
			return
		}
		p = "index.html"
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, p, st.ModTime(), f.(interface {
		Read([]byte) (int, error)
		Seek(int64, int) (int64, error)
	}))
}

func (s *Server) withCommon(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The UI is same-origin, so no CORS is needed and none is granted: that
		// keeps a random web page from driving the local API.
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// withAuth requires the run token for anything but loopback. Binding to the LAN
// so a phone can reach the app must not turn the machine into an open terminal.
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.loopback || s.token == "" {
			next.ServeHTTP(w, r)
			return
		}
		if isLoopbackAddr(r.RemoteAddr) {
			next.ServeHTTP(w, r)
			return
		}
		got := r.Header.Get("X-Go-AI-Team-Token")
		if got == "" {
			got = r.URL.Query().Get("token")
		}
		if got == "" {
			if c, err := r.Cookie("goaiteam_token"); err == nil {
				got = c.Value
			}
		}
		if got != s.token {
			// Accept the token from the query once and set a cookie, so the
			// phone only ever has to open the printed link.
			http.Error(w, "unauthorised: append ?token=<run token> to the URL", http.StatusUnauthorized)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name: "goaiteam_token", Value: s.token, Path: "/",
			HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 30 * 24 * 3600,
		})
		next.ServeHTTP(w, r)
	})
}

func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func decode(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid request body: %w", err)
	}
	return nil
}

func statusFor(err error) int {
	if errors.Is(err, store.ErrNotFound) {
		return http.StatusNotFound
	}
	return http.StatusBadRequest
}

// ---------- accounts ----------

func (s *Server) listAccounts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.accs.Statuses())
}

type createAccountReq struct {
	Name     string         `json:"name"`
	Provider store.Provider `json:"provider"`
	// Dir attaches an existing directory instead of creating a managed one.
	Dir string `json:"dir"`
}

func (s *Server) createAccount(w http.ResponseWriter, r *http.Request) {
	var req createAccountReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var a *store.Account
	var err error
	if strings.TrimSpace(req.Dir) != "" {
		a, err = s.accs.Attach(req.Name, req.Dir, req.Provider)
	} else {
		a, err = s.accs.CreateManaged(req.Name, req.Provider)
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if s.st.Settings().ShareUserLayer {
		if _, err := s.accs.ApplyUserLayer(a); err != nil {
			log.Printf("user layer for %s: %v", a.Name, err)
		}
	}
	writeJSON(w, http.StatusCreated, s.accs.Status(a))
}

type patchAccountReq struct {
	Name                *string `json:"name"`
	Color               *string `json:"color"`
	ExcludeFromFallback *bool   `json:"excludeFromFallback"`
}

func (s *Server) patchAccount(w http.ResponseWriter, r *http.Request) {
	var req patchAccountReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	a, err := s.st.UpdateAccount(r.PathValue("id"), func(a *store.Account) {
		if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
			a.Name = strings.TrimSpace(*req.Name)
		}
		if req.Color != nil {
			a.Color = *req.Color
		}
		if req.ExcludeFromFallback != nil {
			a.ExcludeFromFallback = *req.ExcludeFromFallback
		}
	})
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, s.accs.Status(a))
}

func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a, err := s.st.Account(id)
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	purge := r.URL.Query().Get("purge") == "1"
	if s.st.Settings().ShareUserLayer {
		// Unlink first so a purge can never follow a link out into ~/.claude.
		_ = s.accs.RemoveUserLayer(a)
	}
	if err := s.st.DeleteAccount(id); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	msg := "account unregistered; its directory was left on disk"
	if purge {
		if err := s.accs.DeleteManagedDir(a); err != nil {
			msg = "account unregistered, but the directory was kept: " + err.Error()
		} else {
			msg = "account unregistered and its managed directory deleted"
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "message": msg})
}

func (s *Server) unbenchAccount(w http.ResponseWriter, r *http.Request) {
	a, err := s.st.UpdateAccount(r.PathValue("id"), func(a *store.Account) {
		a.BenchedUntil = time.Time{}
		a.BenchReason = ""
	})
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, s.accs.Status(a))
}

// loginAccount opens a throwaway PTY running the provider CLI against the
// account's own directory. For Claude the user types /login inside it; the
// browser handles OAuth as usual and the badge flips as soon as
// .credentials.json lands on disk. No credential ever passes through this app.
func (s *Server) loginAccount(w http.ResponseWriter, r *http.Request) {
	a, err := s.st.Account(r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	if err := session.EnsureDir(a.Dir); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	cwd, _ := os.UserHomeDir()
	sess, err := s.sm.Spawn(session.SpawnOpts{
		Kind:      session.KindLogin,
		Provider:  a.Provider,
		AccountID: a.ID,
		CWD:       cwd,
		Args:      session.LoginArgs(a.Provider),
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	hint := "Type /login and press Enter, then finish in the browser and paste the code back here."
	if a.Provider != store.ProviderClaude {
		hint = "Complete the sign-in prompts in this terminal."
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"session": sess.Public(),
		"hint":    hint,
	})
}

func (s *Server) discoverAccounts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.accs.Discover())
}

func (s *Server) getUserLayer(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"share":  s.st.Settings().ShareUserLayer,
		"source": accounts.SystemDir(store.ProviderClaude),
		"items":  s.accs.SourceLayer(store.ProviderClaude),
	})
}

func (s *Server) syncUserLayer(w http.ResponseWriter, r *http.Request) {
	if err := s.accs.SyncUserLayer(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---------- folders ----------

func (s *Server) listFolders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.st.Folders())
}

func (s *Server) createFolder(w http.ResponseWriter, r *http.Request) {
	var f store.Folder
	if err := decode(r, &f); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	f.ID = ""
	if strings.TrimSpace(f.Name) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("folder name is required"))
		return
	}
	if err := s.st.AddFolder(&f); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, f)
}

type patchFolderReq struct {
	Name      *string `json:"name"`
	ParentID  *string `json:"parentId"`
	AccountID *string `json:"accountId"`
}

func (s *Server) patchFolder(w http.ResponseWriter, r *http.Request) {
	var req patchFolderReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	f, err := s.st.UpdateFolder(r.PathValue("id"), func(f *store.Folder) {
		if req.Name != nil {
			f.Name = *req.Name
		}
		if req.ParentID != nil {
			f.ParentID = *req.ParentID
		}
		if req.AccountID != nil {
			f.AccountID = *req.AccountID
		}
	})
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

func (s *Server) deleteFolder(w http.ResponseWriter, r *http.Request) {
	if err := s.st.DeleteFolder(r.PathValue("id")); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---------- projects ----------

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.st.Projects())
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var p store.Project
	if err := decode(r, &p); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	p.ID = ""
	p.Path = accounts.ExpandHome(strings.TrimSpace(p.Path))
	if p.Path == "" {
		writeErr(w, http.StatusBadRequest, errors.New("project path is required"))
		return
	}
	abs, err := filepath.Abs(p.Path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	st, err := os.Stat(abs)
	if err != nil || !st.IsDir() {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("%s is not a directory", abs))
		return
	}
	p.Path = abs
	if strings.TrimSpace(p.Name) == "" {
		p.Name = filepath.Base(abs)
	}
	if err := s.st.AddProject(&p); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

type patchProjectReq struct {
	Name      *string `json:"name"`
	FolderID  *string `json:"folderId"`
	AccountID *string `json:"accountId"`
}

func (s *Server) patchProject(w http.ResponseWriter, r *http.Request) {
	var req patchProjectReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	p, err := s.st.UpdateProject(r.PathValue("id"), func(p *store.Project) {
		if req.Name != nil {
			p.Name = *req.Name
		}
		if req.FolderID != nil {
			p.FolderID = *req.FolderID
		}
		if req.AccountID != nil {
			p.AccountID = *req.AccountID
		}
	})
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	if err := s.st.DeleteProject(r.PathValue("id")); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---------- agents ----------

func (s *Server) listAgents(w http.ResponseWriter, r *http.Request) {
	agents := s.st.Agents()
	type row struct {
		*store.Agent
		Resolution store.Resolution       `json:"resolution"`
		Session    *session.PublicSession `json:"session,omitempty"`
	}
	out := make([]row, 0, len(agents))
	for _, a := range agents {
		rw := row{Agent: a, Resolution: s.st.ResolveAccount(a.ID, a.ProjectID, a.Provider)}
		if sess, ok := s.sm.SessionForAgent(a.ID); ok {
			p := sess.Public()
			rw.Session = &p
		}
		out = append(out, rw)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createAgent(w http.ResponseWriter, r *http.Request) {
	var a store.Agent
	if err := decode(r, &a); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	a.ID = ""
	if _, err := s.st.Project(a.ProjectID); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("agent needs an existing projectId"))
		return
	}
	if strings.TrimSpace(a.Name) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("agent name is required"))
		return
	}
	if err := s.st.AddAgent(&a); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

type patchAgentReq struct {
	Name      *string            `json:"name"`
	Role      *string            `json:"role"`
	Color     *string            `json:"color"`
	AccountID *string            `json:"accountId"`
	Model     *string            `json:"model"`
	ExtraArgs *string            `json:"extraArgs"`
	Env       *map[string]string `json:"env"`
}

func (s *Server) patchAgent(w http.ResponseWriter, r *http.Request) {
	var req patchAgentReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	a, err := s.st.UpdateAgent(r.PathValue("id"), func(a *store.Agent) {
		if req.Name != nil {
			a.Name = *req.Name
		}
		if req.Role != nil {
			a.Role = *req.Role
		}
		if req.Color != nil {
			a.Color = *req.Color
		}
		if req.AccountID != nil {
			a.AccountID = *req.AccountID
		}
		if req.Model != nil {
			a.Model = *req.Model
		}
		if req.ExtraArgs != nil {
			a.ExtraArgs = *req.ExtraArgs
		}
		if req.Env != nil {
			a.Env = *req.Env
		}
	})
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"agent":      a,
		"resolution": s.st.ResolveAccount(a.ID, a.ProjectID, a.Provider),
	})
}

func (s *Server) deleteAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if sess, ok := s.sm.SessionForAgent(id); ok {
		_ = s.sm.Remove(sess.ID)
	}
	if err := s.st.DeleteAgent(id); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) resolveAgent(w http.ResponseWriter, r *http.Request) {
	a, err := s.st.Agent(r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, s.st.ResolveAccount(a.ID, a.ProjectID, a.Provider))
}

type startAgentReq struct {
	Cols     uint16 `json:"cols"`
	Rows     uint16 `json:"rows"`
	ResumeID string `json:"resumeId"`
}

func (s *Server) startAgent(w http.ResponseWriter, r *http.Request) {
	var req startAgentReq
	if r.ContentLength > 0 {
		if err := decode(r, &req); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
	}
	a, err := s.st.Agent(r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	p, err := s.st.Project(a.ProjectID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("agent's project no longer exists"))
		return
	}
	if sess, ok := s.sm.SessionForAgent(a.ID); ok {
		writeJSON(w, http.StatusOK, sess.Public())
		return
	}
	sess, err := s.sm.Spawn(session.SpawnOpts{
		Kind:      session.KindAgent,
		AgentID:   a.ID,
		ProjectID: a.ProjectID,
		Provider:  a.Provider,
		CWD:       p.Path,
		Args:      s.sm.AgentArgs(a),
		Env:       a.Env,
		Cols:      req.Cols,
		Rows:      req.Rows,
		ResumeID:  req.ResumeID,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, sess.Public())
}

// ---------- sessions ----------

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	sessions := s.sm.Sessions()
	out := make([]session.PublicSession, 0, len(sessions))
	for _, sess := range sessions {
		out = append(out, sess.Public())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) stopSession(w http.ResponseWriter, r *http.Request) {
	if err := s.sm.Stop(r.PathValue("id")); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) removeSession(w http.ResponseWriter, r *http.Request) {
	if err := s.sm.Remove(r.PathValue("id")); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type inputReq struct {
	Data string `json:"data"`
	// Enter appends a carriage return, which is what submits a prompt.
	Enter bool `json:"enter"`
}

func (s *Server) sendInput(w http.ResponseWriter, r *http.Request) {
	var req inputReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	data := req.Data
	if req.Enter {
		data += "\r"
	}
	if err := s.sm.Write(r.PathValue("id"), []byte(data)); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// sessionScrollback returns a session's buffered terminal output as plain text.
func (s *Server) sessionScrollback(w http.ResponseWriter, r *http.Request) {
	b, err := s.sm.Scrollback(r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(b)
}

// sessionUsage returns the full token breakdown plus the contextual tips, which
// are chosen from the live cache hit rate rather than shown unconditionally.
func (s *Server) sessionUsage(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.sm.Get(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, store.ErrNotFound)
		return
	}
	p := sess.Public()
	writeJSON(w, http.StatusOK, map[string]any{
		"session":      p,
		"durationSecs": int(p.Tokens.Duration().Seconds()),
		"tips":         usageTips(p.Tokens),
	})
}

// Tip is one actionable suggestion with a ready-to-send prompt.
type Tip struct {
	Title  string `json:"title"`
	Body   string `json:"body"`
	Prompt string `json:"prompt"`
}

// usageTips picks advice that matches the numbers instead of listing everything.
func usageTips(t claudefs.TokenStats) []Tip {
	var tips []Tip
	rate := t.CacheHitRate()
	if t.Total() > 0 && rate < 30 {
		tips = append(tips, Tip{
			Title:  "Cache hit rate is low",
			Body:   fmt.Sprintf("At %.0f%% you are paying full input price nearly every turn. Editing CLAUDE.md mid-session, switching model, or reordering early messages busts the cache prefix.", rate),
			Prompt: "Stop editing CLAUDE.md and the early context for now. Keep the prompt prefix stable so the cache can be reused between turns.",
		})
	}
	if t.UserTurns > 0 && t.AssistantTurn > t.UserTurns*8 {
		tips = append(tips, Tip{
			Title:  "Turn ratio looks like a loop",
			Body:   fmt.Sprintf("%d assistant turns for %d prompts. That lopsided ratio usually means the agent is retrying the same fix.", t.AssistantTurn, t.UserTurns),
			Prompt: "Stop and summarise, in three lines: what you have tried, what failed, and what you need from me to unblock this.",
		})
	}
	if t.InputTokens > 400_000 {
		tips = append(tips, Tip{
			Title:  "Fresh input is heavy",
			Body:   "Large whole-file reads are the usual cause. Reading with an offset and limit, or grepping for the symbol first, cuts this sharply.",
			Prompt: "From now on, read files partially with offset and limit, or grep for the symbol first. Do not load whole large files.",
		})
	}
	if t.OutputTokens > 200_000 {
		tips = append(tips, Tip{
			Title:  "Output is heavy",
			Body:   "Write retransmits a whole file; Edit only sends the diff. Preferring Edit lowers both output now and input on the next turn.",
			Prompt: "Prefer the Edit tool over Write for existing files, so only the diff is sent.",
		})
	}
	if t.Total() > 1_500_000 {
		tips = append(tips, Tip{
			Title:  "Consider compacting",
			Body:   "/compact keeps the task and shrinks the history, so the cache prefix survives. /clear wipes it and forces full input pricing on the next turn.",
			Prompt: "/compact",
		})
	}
	if len(tips) == 0 && t.Total() > 0 {
		tips = append(tips, Tip{
			Title: "This session looks healthy",
			Body:  fmt.Sprintf("%.0f%% of input-side tokens are being served from cache. High absolute usage with a good hit rate is normal for long work.", rate),
		})
	}
	return tips
}

// ---------- settings ----------

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.st.Settings())
}

type patchSettingsReq struct {
	DefaultAccountID *string `json:"defaultAccountId"`
	ShareUserLayer   *bool   `json:"shareUserLayer"`
	AutoSwitch       *bool   `json:"autoSwitch"`
	ClaudeBin        *string `json:"claudeBin"`
	SkipPermissions  *bool   `json:"skipPermissions"`
}

func (s *Server) patchSettings(w http.ResponseWriter, r *http.Request) {
	var req patchSettingsReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	layerChanged := false
	next, err := s.st.UpdateSettings(func(st *store.Settings) {
		if req.DefaultAccountID != nil {
			st.DefaultAccountID = *req.DefaultAccountID
		}
		if req.ShareUserLayer != nil && *req.ShareUserLayer != st.ShareUserLayer {
			st.ShareUserLayer = *req.ShareUserLayer
			layerChanged = true
		}
		if req.AutoSwitch != nil {
			st.AutoSwitch = *req.AutoSwitch
		}
		if req.ClaudeBin != nil {
			st.ClaudeBin = strings.TrimSpace(*req.ClaudeBin)
		}
		if req.SkipPermissions != nil {
			st.SkipPermissions = *req.SkipPermissions
		}
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if layerChanged {
		if err := s.accs.SyncUserLayer(); err != nil {
			log.Printf("user layer sync: %v", err)
		}
	}
	writeJSON(w, http.StatusOK, next)
}

// ---------- doctor ----------

// doctor answers "why would an agent not start" before the user has to guess.
func (s *Server) doctor(w http.ResponseWriter, r *http.Request) {
	type check struct {
		Name   string `json:"name"`
		OK     bool   `json:"ok"`
		Detail string `json:"detail"`
		Fix    string `json:"fix,omitempty"`
	}
	var checks []check

	bin := store.ProviderClaude.Bin()
	if cb := s.st.Settings().ClaudeBin; cb != "" {
		bin = cb
	}
	if path, err := lookPath(bin); err == nil {
		checks = append(checks, check{"claude CLI on PATH", true, path, ""})
	} else {
		checks = append(checks, check{
			"claude CLI on PATH", false, err.Error(),
			"Install Claude Code, or set a full path in Settings if it lives somewhere unusual.",
		})
	}

	sys := accounts.SystemDir(store.ProviderClaude)
	sysCred := claudefs.ReadCredentials(sys)
	switch sysCred.State {
	case claudefs.AuthActive, claudefs.AuthRefreshable:
		checks = append(checks, check{"system account signed in", true, sys, ""})
	case claudefs.AuthLoggedOut:
		checks = append(checks, check{
			"system account signed in", false,
			sys + " holds a credentials file with blank tokens (signed out)",
			"Run claude there and type /login, or point this account at a directory that is signed in.",
		})
	default:
		checks = append(checks, check{
			"system account signed in", false, "no credentials in " + sys,
			"Run claude and type /login once, or add an account here and sign in from the app.",
		})
	}

	accs := s.st.AccountsFor(store.ProviderClaude)
	signed, husks := 0, 0
	for _, a := range accs {
		switch claudefs.ReadCredentials(a.Dir).State {
		case claudefs.AuthActive, claudefs.AuthRefreshable:
			signed++
		case claudefs.AuthLoggedOut:
			husks++
		}
	}
	detail := fmt.Sprintf("%d of %d registered accounts hold usable credentials", signed, len(accs))
	if husks > 0 {
		// Worth calling out separately: a signed-out directory looks configured
		// and is the most confusing way for an agent to fail.
		detail += fmt.Sprintf("; %d are signed out (file present, tokens blank)", husks)
	}
	checks = append(checks, check{
		"Claude accounts signed in", signed > 0, detail,
		"Use Sign in on any account whose badge says logged out.",
	})
	checks = append(checks, check{
		"auto-switch has somewhere to go", signed >= 2,
		fmt.Sprintf("%d usable accounts; auto-switch needs at least 2", signed),
		"Add a second Claude account to keep working through a usage limit.",
	})

	if runtime.GOOS == "windows" {
		checks = append(checks, check{
			"config sharing method", true,
			"directory junctions (mklink /J), which need no elevation on Windows", "",
		})
	} else {
		checks = append(checks, check{"config sharing method", true, "symlinks", ""})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"checks":   checks,
		"platform": runtime.GOOS + "/" + runtime.GOARCH,
		"home":     s.st.RootDir(),
	})
}

// ---------- filesystem browse ----------

// listDir powers the project picker. It is read-only and returns directories
// only, which is all the UI needs to choose a working directory.
func (s *Server) listDir(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if p == "" {
		p, _ = os.UserHomeDir()
	}
	p = accounts.ExpandHome(p)
	abs, err := filepath.Abs(p)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	type entry struct {
		Name  string `json:"name"`
		Path  string `json:"path"`
		IsGit bool   `json:"isGit"`
	}
	var dirs []entry
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		full := filepath.Join(abs, e.Name())
		gi := false
		if st, err := os.Stat(filepath.Join(full, ".git")); err == nil && st.IsDir() {
			gi = true
		}
		dirs = append(dirs, entry{Name: e.Name(), Path: full, IsGit: gi})
	}
	sort.Slice(dirs, func(i, j int) bool { return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name) })
	writeJSON(w, http.StatusOK, map[string]any{
		"path":   abs,
		"parent": filepath.Dir(abs),
		"dirs":   dirs,
	})
}

// LocalIPs lists the IPv4 addresses a phone could actually reach, best first.
//
// Link-local 169.254.x.x addresses are excluded. Windows hands them out to every
// idle or virtual adapter, and they sort before the real one, so printing them
// would put a dead link at the top of the banner — the user taps it, nothing
// answers, and the feature looks broken.
func LocalIPs() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		v4 := ipnet.IP.To4()
		if v4 == nil || v4.IsLoopback() || v4.IsLinkLocalUnicast() {
			continue
		}
		out = append(out, v4.String())
	}
	// Rank by how likely the address is to be the one on the user's home
	// network, so the first line of the banner is the one worth trying.
	sort.SliceStable(out, func(i, j int) bool {
		return lanRank(out[i]) < lanRank(out[j])
	})
	return out
}

// lanRank orders candidate addresses: ordinary home networks first, then other
// private ranges, then hypervisor and container networks, which are real but
// almost never the one a phone is on.
func lanRank(ip string) int {
	switch {
	case strings.HasPrefix(ip, "192.168.56."): // VirtualBox host-only
		return 40
	case strings.HasPrefix(ip, "172.17."), strings.HasPrefix(ip, "172.18."): // Docker
		return 30
	case strings.HasPrefix(ip, "192.168."):
		return 10
	case strings.HasPrefix(ip, "10."):
		return 20
	default:
		return 25
	}
}

// PortString renders a port for a URL.
func PortString(p int) string { return strconv.Itoa(p) }
