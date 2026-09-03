package session

import (
	"fmt"

	"github.com/uniair/go-ai-team/internal/store"
)

// KindRemote is a terminal on another machine over SSH.
const KindRemote Kind = "remote"

// SpawnRemote opens a terminal on a remote host using the user's own ssh
// client, so their config, agent, jump hosts and known_hosts all apply — none
// of which we would inherit by dialling the connection ourselves.
func (m *Manager) SpawnRemote(label string, argv []string, cols, rows uint16) (*Session, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("no ssh command to run")
	}
	return m.spawnRaw(spawnRawOpts{
		Kind:     KindRemote,
		Label:    label,
		Provider: store.ProviderClaude,
		Bin:      argv[0],
		Args:     argv[1:],
		Cols:     cols,
		Rows:     rows,
	})
}
