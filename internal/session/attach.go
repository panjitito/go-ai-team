package session

import "path/filepath"

// Where pasted images live.
//
// Deliberately outside the project: a pasted screenshot must never leave a file
// in somebody's repository for them to commit by accident. That means it is also
// outside the directory the CLI reads by default, so sessions are launched with
// this root added to the ones they may read — see attachArgs. Without that,
// every single paste would stop on a permission prompt.
func AttachRoot(stateDir string) string { return filepath.Join(stateDir, "pastes") }

// AttachDir is where one session's images go, so they can be found and cleared
// per conversation rather than as one growing pile.
func AttachDir(stateDir, sessionID string) string {
	return filepath.Join(AttachRoot(stateDir), sessionID)
}
