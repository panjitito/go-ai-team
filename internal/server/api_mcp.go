package server

import (
	"fmt"
	"net/http"

	"github.com/uniair/go-ai-team/internal/claudefs"
)

// MCP servers, per account.
//
// The CLI manages these one config directory at a time, which is the right shape
// for the CLI and the wrong one here: this app exists because you have several
// accounts, and the question it has to answer is "which of them can reach my
// database". Worse, .claude.json is not part of the shared user layer — it
// cannot be, it also holds session history and per-project state — so a freshly
// signed-in account has none of your servers and nothing says so. The tools are
// simply absent and the agent reports it cannot connect.
//
// So: an inventory across accounts, and a copy between them.
//
// Nothing writes while an account is in use. The CLI owns that file and writes
// it as it runs, and a rewrite underneath a live session is how a configuration
// gets lost.

type mcpAccountView struct {
	AccountID string               `json:"accountId"`
	Name      string               `json:"name"`
	Dir       string               `json:"dir"`
	Servers   []claudefs.MCPServer `json:"servers"`
	// Busy is true while a session is running on this account, which is when it
	// must not be written to.
	Busy bool `json:"busy"`
	// Error is set when this one account could not be read, so the others still
	// list rather than the whole panel failing.
	Error string `json:"error,omitempty"`
}

// listMCP reports every account's servers.
func (s *Server) listMCP(w http.ResponseWriter, r *http.Request) {
	out := []mcpAccountView{}
	for _, a := range s.st.Accounts() {
		v := mcpAccountView{AccountID: a.ID, Name: a.Name, Dir: a.Dir, Servers: []claudefs.MCPServer{}}
		if a.Dir == "" {
			v.Error = "this account has no configuration directory yet"
			out = append(out, v)
			continue
		}
		servers, err := claudefs.ListMCPServers(a.Dir)
		if err != nil {
			v.Error = err.Error()
		} else if servers != nil {
			v.Servers = servers
		}
		v.Busy = s.accountBusy(a.ID)
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
}

// accountBusy reports whether anything is running on an account.
func (s *Server) accountBusy(id string) bool {
	for _, sess := range s.sm.Sessions() {
		p := sess.Public()
		if p.AccountID == id && p.Status != "exited" && p.Status != "error" {
			return true
		}
	}
	return false
}

type copyMCPReq struct {
	From  string   `json:"from"`
	To    string   `json:"to"`
	Names []string `json:"names"`
}

// copyMCP copies server definitions from one account to another.
func (s *Server) copyMCP(w http.ResponseWriter, r *http.Request) {
	var req copyMCPReq
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if len(req.Names) == 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("pick at least one server to copy"))
		return
	}
	from, err := s.st.Account(req.From)
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	to, err := s.st.Account(req.To)
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	if from.Dir == "" || to.Dir == "" {
		writeErr(w, http.StatusBadRequest,
			fmt.Errorf("both accounts need a configuration directory; sign in first"))
		return
	}
	// The CLI writes this file as it runs. Rewriting it underneath a live
	// session is how a configuration gets lost.
	if s.accountBusy(to.ID) {
		writeErr(w, http.StatusConflict,
			fmt.Errorf("%s has a session running — stop it before changing its configuration", to.Name))
		return
	}

	copied, skipped, err := claudefs.CopyMCPServers(from.Dir, to.Dir, req.Names)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"copied":  orEmptyStrings(copied),
		"skipped": orEmptyStrings(skipped),
	})
}

// orEmptyStrings keeps a list endpoint answering with [] rather than null.
func orEmptyStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
