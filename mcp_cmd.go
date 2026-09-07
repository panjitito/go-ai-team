package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/panjitito/go-ai-team/internal/dbx"
	"github.com/panjitito/go-ai-team/internal/mcp"
	"github.com/panjitito/go-ai-team/internal/secrets"
	"github.com/panjitito/go-ai-team/internal/session"
	"github.com/panjitito/go-ai-team/internal/sshx"
	"github.com/panjitito/go-ai-team/internal/store"
)

// runMCP serves the app's own tools to the agent that spawned this process.
//
// It deliberately does not talk to the running server over HTTP. Both processes
// read the same state file, and going through the socket would mean an agent's
// tool calls were authenticated by the LAN token — which an agent should never
// hold. Sharing the file instead makes the process boundary the trust boundary:
// this instance can only reach what its --project scope allows.
func runMCP(args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	var (
		projectID = fs.String("project", "", "project id this agent is working in (required)")
		agentID   = fs.String("agent", "", "agent id, so messages and memory are attributed")
		agentName = fs.String("agent-name", "agent", "display name for attribution")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *projectID == "" {
		return errors.New("--project is required so tools cannot reach another project's data")
	}

	st, err := store.Open()
	if err != nil {
		return fmt.Errorf("cannot open the store: %w", err)
	}
	project, err := st.Project(*projectID)
	if err != nil {
		return fmt.Errorf("no project %s", *projectID)
	}

	// This process spawns nothing long-lived, so it gets a manager only so that
	// command_run has somewhere to execute.
	sm := session.NewManager(st, nil)

	var vault *secrets.Vault
	if secrets.Available() {
		vault, _ = secrets.Open(st.RootDir())
	}
	resolveSecret := func(name string) (string, error) {
		if vault == nil {
			return "", errors.New("the secret vault is unavailable")
		}
		return vault.Resolve(name)
	}
	ssh := sshx.New(st, resolveSecret)
	db := dbx.New(st, resolveSecret, ssh.Tunnel)

	srv := mcp.NewServer("go-ai-team", version)
	mcp.Register(srv, mcp.Deps{
		Store:     st,
		ProjectID: *projectID,
		AgentID:   *agentID,
		AgentName: *agentName,

		RunCommand: func(ctx context.Context, id string) (string, error) {
			c, err := st.Command(id)
			if err != nil {
				return "", err
			}
			return sm.RunCommandOnce(ctx, c, project.Path, nil)
		},
		SecretNames: func() ([]string, error) {
			if vault == nil {
				return nil, errors.New("the secret vault is unavailable")
			}
			return vault.Names()
		},
		Query: func(ctx context.Context, connName, sqlText string) (string, error) {
			// An agent never gets the write path: confirmWrite is false here by
			// construction, so a write is refused however the statement is
			// phrased.
			res, err := db.Query(ctx, connName, sqlText, false)
			if err != nil {
				return "", err
			}
			return res.Render(), nil
		},
	})

	return srv.Serve(context.Background(), os.Stdin, os.Stdout)
}
