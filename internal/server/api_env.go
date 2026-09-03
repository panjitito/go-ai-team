package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/uniair/go-ai-team/internal/automation"
	"github.com/uniair/go-ai-team/internal/guard"
	"github.com/uniair/go-ai-team/internal/secrets"
	"github.com/uniair/go-ai-team/internal/session"
	"github.com/uniair/go-ai-team/internal/store"
)

// Dev commands, secrets, databases, SSH, schedules, webhooks and the process
// guard: the environment half of the app.

func (s *Server) routeEnv(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/commands", s.listCommands)
	mux.HandleFunc("POST /api/commands", s.createCommand)
	mux.HandleFunc("PATCH /api/commands/{id}", s.patchCommand)
	mux.HandleFunc("DELETE /api/commands/{id}", s.deleteCommand)
	mux.HandleFunc("POST /api/commands/{id}/start", s.startCommand)

	mux.HandleFunc("GET /api/secrets", s.listSecrets)
	mux.HandleFunc("PUT /api/secrets", s.putSecret)
	mux.HandleFunc("DELETE /api/secrets/{name}", s.deleteSecret)

	mux.HandleFunc("GET /api/dbconns", s.listDBConns)
	mux.HandleFunc("POST /api/dbconns", s.createDBConn)
	mux.HandleFunc("PATCH /api/dbconns/{id}", s.patchDBConn)
	mux.HandleFunc("DELETE /api/dbconns/{id}", s.deleteDBConn)
	mux.HandleFunc("POST /api/dbconns/{id}/query", s.queryDB)

	mux.HandleFunc("GET /api/sshhosts", s.listSSHHosts)
	mux.HandleFunc("POST /api/sshhosts", s.createSSHHost)
	mux.HandleFunc("PATCH /api/sshhosts/{id}", s.patchSSHHost)
	mux.HandleFunc("DELETE /api/sshhosts/{id}", s.deleteSSHHost)
	mux.HandleFunc("POST /api/sshhosts/{id}/test", s.testSSHHost)
	mux.HandleFunc("POST /api/sshhosts/{id}/shell", s.sshShell)

	mux.HandleFunc("GET /api/schedules", s.listSchedules)
	mux.HandleFunc("POST /api/schedules", s.createSchedule)
	mux.HandleFunc("PATCH /api/schedules/{id}", s.patchSchedule)
	mux.HandleFunc("DELETE /api/schedules/{id}", s.deleteSchedule)
	mux.HandleFunc("POST /api/schedules/{id}/run", s.runSchedule)

	mux.HandleFunc("GET /api/webhooks", s.listWebhooks)
	mux.HandleFunc("POST /api/webhooks", s.createWebhook)
	mux.HandleFunc("PATCH /api/webhooks/{id}", s.patchWebhook)
	mux.HandleFunc("DELETE /api/webhooks/{id}", s.deleteWebhook)
	mux.HandleFunc("POST /api/webhooks/{id}/test", s.testWebhook)

	// The public delivery endpoint. Deliberately outside /api so it reads as
	// what it is, and exempt from the LAN token because the signing secret is
	// what authenticates it.
	mux.HandleFunc("POST /hooks/{token}", s.receiveWebhook)

	mux.HandleFunc("GET /api/guard", s.processGuard)
	mux.HandleFunc("POST /api/guard/kill", s.guardKill)
}

// ---------- dev commands ----------

func (s *Server) listCommands(w http.ResponseWriter, r *http.Request) {
	pid := r.URL.Query().Get("projectId")
	if pid == "" {
		writeJSON(w, http.StatusOK, orEmpty(s.st.Commands()))
		return
	}
	writeJSON(w, http.StatusOK, orEmpty(s.st.CommandsFor(pid)))
}

func (s *Server) createCommand(w http.ResponseWriter, r *http.Request) {
	var c store.DevCommand
	if err := decode(r, &c); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	c.ID = ""
	if strings.TrimSpace(c.Name) == "" || strings.TrimSpace(c.Command) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("a command needs a name and a command line"))
		return
	}
	if _, err := s.st.Project(c.ProjectID); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("a command needs an existing projectId"))
		return
	}
	if err := s.st.AddCommand(&c); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

type patchCommandReq struct {
	Name      *string            `json:"name"`
	Command   *string            `json:"command"`
	Dir       *string            `json:"dir"`
	Env       *map[string]string `json:"env"`
	Autostart *bool              `json:"autostart"`
	Order     *int               `json:"order"`
}

func (s *Server) patchCommand(w http.ResponseWriter, r *http.Request) {
	var req patchCommandReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	c, err := s.st.UpdateCommand(r.PathValue("id"), func(c *store.DevCommand) {
		if req.Name != nil {
			c.Name = *req.Name
		}
		if req.Command != nil {
			c.Command = *req.Command
		}
		if req.Dir != nil {
			c.Dir = *req.Dir
		}
		if req.Env != nil {
			c.Env = *req.Env
		}
		if req.Autostart != nil {
			c.Autostart = *req.Autostart
		}
		if req.Order != nil {
			c.Order = *req.Order
		}
	})
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) deleteCommand(w http.ResponseWriter, r *http.Request) {
	if err := s.st.DeleteCommand(r.PathValue("id")); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type startCommandReq struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// startCommand runs a saved command in a live terminal. Secret references in
// its environment are resolved here, in this process, and go straight into the
// child's environment.
func (s *Server) startCommand(w http.ResponseWriter, r *http.Request) {
	var req startCommandReq
	if r.ContentLength > 0 {
		if err := decode(r, &req); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
	}
	c, err := s.st.Command(r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	p, err := s.st.Project(c.ProjectID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("this command's project no longer exists"))
		return
	}

	env, err := s.expandSecrets(c.Env)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	sess, err := s.sm.SpawnCommand(c, p.Path, env, req.Cols, req.Rows)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, sess.Public())
}

// expandSecrets resolves {{secret:NAME}} references without ever returning a
// value to the caller: the map goes straight into a child environment.
func (s *Server) expandSecrets(in map[string]string) (map[string]string, error) {
	if len(in) == 0 || s.vault == nil {
		return in, nil
	}
	return s.vault.ExpandEnv(in)
}

// ---------- secrets ----------

// listSecrets returns names only. There is deliberately no endpoint that
// returns a value: see internal/secrets.
func (s *Server) listSecrets(w http.ResponseWriter, r *http.Request) {
	if s.vault == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"available": false,
			"reason":    "this machine has no usable encryption provider, so the vault is disabled rather than stored in clear text",
			"secrets":   []any{},
		})
		return
	}
	names, err := s.vault.Names()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	metas := s.st.SecretMetas()
	byName := map[string]store.SecretMeta{}
	for _, m := range metas {
		byName[m.Name] = m
	}
	type row struct {
		Name      string    `json:"name"`
		Note      string    `json:"note,omitempty"`
		ProjectID string    `json:"projectId,omitempty"`
		UpdatedAt time.Time `json:"updatedAt,omitempty"`
	}
	out := make([]row, 0, len(names))
	for _, n := range names {
		m := byName[n]
		out = append(out, row{Name: n, Note: m.Note, ProjectID: m.ProjectID, UpdatedAt: m.UpdatedAt})
	}
	writeJSON(w, http.StatusOK, map[string]any{"available": true, "secrets": out})
}

type putSecretReq struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	Note      string `json:"note"`
	ProjectID string `json:"projectId"`
}

func (s *Server) putSecret(w http.ResponseWriter, r *http.Request) {
	if s.vault == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New(
			"the vault is unavailable on this machine, and writing secrets in clear text is refused"))
		return
	}
	var req putSecretReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("a secret needs a name"))
		return
	}
	if req.Value == "" {
		writeErr(w, http.StatusBadRequest, errors.New("a secret needs a value"))
		return
	}
	if err := s.vault.Set(req.Name, req.Value); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.st.UpsertSecretMeta(store.SecretMeta{
		Name: req.Name, Note: req.Note, ProjectID: req.ProjectID,
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// The response deliberately echoes the name and nothing else.
	writeJSON(w, http.StatusOK, map[string]string{"name": req.Name, "status": "stored"})
}

func (s *Server) deleteSecret(w http.ResponseWriter, r *http.Request) {
	if s.vault == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("the vault is unavailable"))
		return
	}
	name := r.PathValue("name")
	if err := s.vault.Delete(name); err != nil && !errors.Is(err, secrets.ErrNotFound) {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	_ = s.st.DeleteSecretMeta(name)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---------- databases ----------

// listDBConns returns connections without any credential. SecretRef is a name,
// never a value.
func (s *Server) listDBConns(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, orEmpty(s.st.DBConns()))
}

func (s *Server) createDBConn(w http.ResponseWriter, r *http.Request) {
	var c store.DBConn
	if err := decode(r, &c); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	c.ID = ""
	if strings.TrimSpace(c.Name) == "" || strings.TrimSpace(c.Host) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("a connection needs a name and a host"))
		return
	}
	switch c.Driver {
	case "mysql", "postgres", "mongodb":
	default:
		writeErr(w, http.StatusBadRequest, errors.New("driver must be mysql, postgres or mongodb"))
		return
	}
	if _, exists := s.st.DBConnByName(c.Name); exists {
		writeErr(w, http.StatusConflict, fmt.Errorf("a connection named %q already exists", c.Name))
		return
	}
	if err := s.st.AddDBConn(&c); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

type patchDBConnReq struct {
	Name         *string `json:"name"`
	Host         *string `json:"host"`
	Port         *int    `json:"port"`
	User         *string `json:"user"`
	DBName       *string `json:"dbName"`
	TLS          *bool   `json:"tls"`
	SecretRef    *string `json:"secretRef"`
	Writable     *bool   `json:"writable"`
	Production   *bool   `json:"production"`
	TunnelHostID *string `json:"tunnelHostId"`
}

func (s *Server) patchDBConn(w http.ResponseWriter, r *http.Request) {
	var req patchDBConnReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	c, err := s.st.UpdateDBConn(r.PathValue("id"), func(c *store.DBConn) {
		if req.Name != nil {
			c.Name = *req.Name
		}
		if req.Host != nil {
			c.Host = *req.Host
		}
		if req.Port != nil {
			c.Port = *req.Port
		}
		if req.User != nil {
			c.User = *req.User
		}
		if req.DBName != nil {
			c.DBName = *req.DBName
		}
		if req.TLS != nil {
			c.TLS = *req.TLS
		}
		if req.SecretRef != nil {
			c.SecretRef = *req.SecretRef
		}
		if req.Writable != nil {
			c.Writable = *req.Writable
		}
		if req.Production != nil {
			c.Production = *req.Production
		}
		if req.TunnelHostID != nil {
			c.TunnelHostID = *req.TunnelHostID
		}
	})
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) deleteDBConn(w http.ResponseWriter, r *http.Request) {
	if err := s.st.DeleteDBConn(r.PathValue("id")); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type queryDBReq struct {
	SQL string `json:"sql"`
	// Confirm is required for anything that changes data, and is separate from
	// the connection being writable: a writable connection should still not be
	// modified by a query someone meant to be a read.
	Confirm bool `json:"confirm"`
}

func (s *Server) queryDB(w http.ResponseWriter, r *http.Request) {
	var req queryDBReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	c, err := s.st.DBConn(r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	if s.db == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("database access is not configured"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	res, err := s.db.Query(ctx, c.Name, req.SQL, req.Confirm)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"result": res,
		"table":  res.Render(),
		"kind":   res.Kind,
	})
}

// ---------- ssh ----------

func (s *Server) listSSHHosts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, orEmpty(s.st.SSHHosts()))
}

func (s *Server) createSSHHost(w http.ResponseWriter, r *http.Request) {
	var h store.SSHHost
	if err := decode(r, &h); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	h.ID = ""
	if strings.TrimSpace(h.Name) == "" || strings.TrimSpace(h.Host) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("a host needs a name and an address"))
		return
	}
	if err := s.st.AddSSHHost(&h); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, h)
}

type patchSSHHostReq struct {
	Name      *string `json:"name"`
	Host      *string `json:"host"`
	Port      *int    `json:"port"`
	User      *string `json:"user"`
	KeyPath   *string `json:"keyPath"`
	SecretRef *string `json:"secretRef"`
	ProjectID *string `json:"projectId"`
}

func (s *Server) patchSSHHost(w http.ResponseWriter, r *http.Request) {
	var req patchSSHHostReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	h, err := s.st.UpdateSSHHost(r.PathValue("id"), func(h *store.SSHHost) {
		if req.Name != nil {
			h.Name = *req.Name
		}
		if req.Host != nil {
			h.Host = *req.Host
		}
		if req.Port != nil {
			h.Port = *req.Port
		}
		if req.User != nil {
			h.User = *req.User
		}
		if req.KeyPath != nil {
			h.KeyPath = *req.KeyPath
		}
		if req.SecretRef != nil {
			h.SecretRef = *req.SecretRef
		}
		if req.ProjectID != nil {
			h.ProjectID = *req.ProjectID
		}
	})
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, h)
}

func (s *Server) deleteSSHHost(w http.ResponseWriter, r *http.Request) {
	if err := s.st.DeleteSSHHost(r.PathValue("id")); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) testSSHHost(w http.ResponseWriter, r *http.Request) {
	if s.ssh == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("ssh is not configured"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	out, err := s.ssh.Test(ctx, r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "connected", "output": strings.TrimSpace(out)})
}

type sshShellReq struct {
	Command string `json:"command"`
	Cols    uint16 `json:"cols"`
	Rows    uint16 `json:"rows"`
}

// sshShell opens a terminal on a remote host using the user's own ssh client,
// so their config, agent, jump hosts and known_hosts all apply.
func (s *Server) sshShell(w http.ResponseWriter, r *http.Request) {
	var req sshShellReq
	if r.ContentLength > 0 {
		if err := decode(r, &req); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
	}
	if s.ssh == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("ssh is not configured"))
		return
	}
	h, err := s.st.SSHHost(r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	argv, err := s.ssh.SSHCommand(h.ID, req.Command)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	sess, err := s.sm.SpawnRemote(h.Name, argv, req.Cols, req.Rows)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, sess.Public())
}

// ---------- schedules ----------

func (s *Server) listSchedules(w http.ResponseWriter, r *http.Request) {
	type row struct {
		*store.Schedule
		Cadence string `json:"cadence"`
	}
	scs := s.st.Schedules()
	out := make([]row, 0, len(scs))
	for _, sc := range scs {
		out = append(out, row{sc, automation.Describe(sc)})
	}
	writeJSON(w, http.StatusOK, orEmpty(out))
}

func (s *Server) createSchedule(w http.ResponseWriter, r *http.Request) {
	var sc store.Schedule
	if err := decode(r, &sc); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	sc.ID = ""
	if strings.TrimSpace(sc.Name) == "" || strings.TrimSpace(sc.Prompt) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("a schedule needs a name and a prompt"))
		return
	}
	if _, err := s.st.Project(sc.ProjectID); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("a schedule needs an existing projectId"))
		return
	}
	switch sc.Every {
	case "minutes", "hourly", "daily", "weekly", "monthly":
	default:
		writeErr(w, http.StatusBadRequest,
			errors.New("cadence must be minutes, hourly, daily, weekly or monthly"))
		return
	}
	sc.NextRun = automation.NextRun(&sc, time.Now())
	if err := s.st.AddSchedule(&sc); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, sc)
}

type patchScheduleReq struct {
	Name    *string `json:"name"`
	Prompt  *string `json:"prompt"`
	AgentID *string `json:"agentId"`
	Every   *string `json:"every"`
	N       *int    `json:"n"`
	Minute  *int    `json:"minute"`
	Hour    *int    `json:"hour"`
	Wday    *int    `json:"wday"`
	Mday    *int    `json:"mday"`
	Enabled *bool   `json:"enabled"`
}

func (s *Server) patchSchedule(w http.ResponseWriter, r *http.Request) {
	var req patchScheduleReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	sc, err := s.st.UpdateSchedule(r.PathValue("id"), func(sc *store.Schedule) {
		if req.Name != nil {
			sc.Name = *req.Name
		}
		if req.Prompt != nil {
			sc.Prompt = *req.Prompt
		}
		if req.AgentID != nil {
			sc.AgentID = *req.AgentID
		}
		if req.Every != nil {
			sc.Every = *req.Every
		}
		if req.N != nil {
			sc.N = *req.N
		}
		if req.Minute != nil {
			sc.Minute = *req.Minute
		}
		if req.Hour != nil {
			sc.Hour = *req.Hour
		}
		if req.Wday != nil {
			sc.Wday = *req.Wday
		}
		if req.Mday != nil {
			sc.Mday = *req.Mday
		}
		if req.Enabled != nil {
			sc.Enabled = *req.Enabled
		}
		// Any cadence change invalidates the pending window.
		sc.NextRun = automation.NextRun(sc, time.Now())
	})
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, sc)
}

func (s *Server) deleteSchedule(w http.ResponseWriter, r *http.Request) {
	if err := s.st.DeleteSchedule(r.PathValue("id")); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) runSchedule(w http.ResponseWriter, r *http.Request) {
	sid, err := s.sched.RunNow(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"sessionId": sid})
}

// ---------- webhooks ----------

func (s *Server) listWebhooks(w http.ResponseWriter, r *http.Request) {
	type row struct {
		*store.Webhook
		URL string `json:"url"`
	}
	whs := s.st.Webhooks()
	out := make([]row, 0, len(whs))
	for _, wh := range whs {
		out = append(out, row{wh, "/hooks/" + wh.Token})
	}
	writeJSON(w, http.StatusOK, orEmpty(out))
}

func (s *Server) createWebhook(w http.ResponseWriter, r *http.Request) {
	var wh store.Webhook
	if err := decode(r, &wh); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	wh.ID = ""
	if strings.TrimSpace(wh.Name) == "" || strings.TrimSpace(wh.Prompt) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("a trigger needs a name and a prompt"))
		return
	}
	if _, err := s.st.Project(wh.ProjectID); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("a trigger needs an existing projectId"))
		return
	}
	// The token and secret are generated here, not accepted from the client: a
	// caller-chosen token could be guessable, and the whole gate rests on it.
	wh.Token = randomHex(16)
	if wh.Secret == "" {
		wh.Secret = randomHex(24)
	}
	if wh.BurstLimit <= 0 {
		wh.BurstLimit = 10
	}
	if err := s.st.AddWebhook(&wh); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"webhook": wh, "url": "/hooks/" + wh.Token})
}

type patchWebhookReq struct {
	Name       *string `json:"name"`
	Prompt     *string `json:"prompt"`
	AgentID    *string `json:"agentId"`
	Filter     *string `json:"filter"`
	BurstLimit *int    `json:"burstLimit"`
	Enabled    *bool   `json:"enabled"`
	Rotate     *bool   `json:"rotate"`
}

func (s *Server) patchWebhook(w http.ResponseWriter, r *http.Request) {
	var req patchWebhookReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// A filter that cannot be parsed would silently stop every delivery, so it
	// is validated before being saved rather than at delivery time.
	if req.Filter != nil && strings.TrimSpace(*req.Filter) != "" {
		if _, err := automation.Match(*req.Filter, map[string]string{}); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
	}
	wh, err := s.st.UpdateWebhook(r.PathValue("id"), func(wh *store.Webhook) {
		if req.Name != nil {
			wh.Name = *req.Name
		}
		if req.Prompt != nil {
			wh.Prompt = *req.Prompt
		}
		if req.AgentID != nil {
			wh.AgentID = *req.AgentID
		}
		if req.Filter != nil {
			wh.Filter = *req.Filter
		}
		if req.BurstLimit != nil && *req.BurstLimit > 0 {
			wh.BurstLimit = *req.BurstLimit
		}
		if req.Enabled != nil {
			wh.Enabled = *req.Enabled
		}
		if req.Rotate != nil && *req.Rotate {
			wh.Token = randomHex(16)
			wh.Secret = randomHex(24)
		}
	})
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"webhook": wh, "url": "/hooks/" + wh.Token})
}

func (s *Server) deleteWebhook(w http.ResponseWriter, r *http.Request) {
	if err := s.st.DeleteWebhook(r.PathValue("id")); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// testWebhook sends a synthetic delivery through the whole pipeline so a
// trigger can be verified without waiting for a real event.
func (s *Server) testWebhook(w http.ResponseWriter, r *http.Request) {
	wh, err := s.st.Webhook(r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	body := []byte(`{"action":"opened","number":1,"title":"Test delivery from Go AI Team",` +
		`"pull_request":{"title":"Test delivery","user":{"login":"you"}}}`)
	// Sign it the way GitHub would, so the signature path is exercised too.
	headers := map[string]string{
		"x-hub-signature-256": "sha256=" + automation.SignHex(wh.Secret, body),
		"x-github-event":      "pull_request",
	}
	res := s.hooks.Deliver(wh, body, headers)
	writeJSON(w, http.StatusOK, res)
}

// receiveWebhook is the public delivery endpoint.
func (s *Server) receiveWebhook(w http.ResponseWriter, r *http.Request) {
	wh, ok := s.st.WebhookByToken(r.PathValue("token"))
	if !ok {
		// Deliberately vague: a precise answer would let someone enumerate
		// valid trigger tokens.
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	body, err := readLimited(r, 1<<20)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	headers := map[string]string{}
	for k, v := range r.Header {
		if len(v) > 0 {
			headers[strings.ToLower(k)] = v[0]
		}
	}
	res := s.hooks.Deliver(wh, body, headers)
	code := http.StatusOK
	if !res.Accepted && !res.Queued && strings.Contains(res.Reason, "signature") {
		code = http.StatusUnauthorized
	}
	writeJSON(w, code, res)
}

// ---------- process guard ----------

func (s *Server) processGuard(w http.ResponseWriter, r *http.Request) {
	roots := s.guardRoots()
	if len(roots) == 0 {
		writeJSON(w, http.StatusOK, guard.Report{Supported: true, At: time.Now()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	rep := guard.Sweep(ctx, roots)
	rep.Procs = orEmpty(rep.Procs)
	writeJSON(w, http.StatusOK, rep)
}

// guardRoots is every live agent's process, plus recently exited ones whose
// children may have outlived them.
func (s *Server) guardRoots() []guard.Root {
	var roots []guard.Root
	for _, sess := range s.sm.Sessions() {
		p := sess.Public()
		if p.PID == 0 {
			continue
		}
		if p.Kind != session.KindAgent && p.Kind != session.KindCommand {
			continue
		}
		alive := p.Status != session.StatusExited && p.Status != session.StatusError
		name := p.Label
		if name == "" {
			if a, err := s.st.Agent(p.AgentID); err == nil {
				name = a.Name
			}
		}
		roots = append(roots, guard.Root{
			PID: p.PID, AgentID: p.AgentID, AgentName: name, Alive: alive,
		})
	}
	return roots
}

type guardKillReq struct {
	PID int `json:"pid"`
}

func (s *Server) guardKill(w http.ResponseWriter, r *http.Request) {
	var req guardKillReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// Never end a process we are managing as a session: that is what Stop is
	// for, and it cleans up properly.
	for _, sess := range s.sm.Sessions() {
		if sess.Public().PID == req.PID {
			writeErr(w, http.StatusBadRequest,
				errors.New("that is a managed terminal; stop it from its own tab instead"))
			return
		}
	}
	if err := guard.Kill(req.PID); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ended"})
}
