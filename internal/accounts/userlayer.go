package accounts

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/panjitito/go-ai-team/internal/claudefs"
	"github.com/panjitito/go-ai-team/internal/store"
)

// The user layer problem: signing an account in gives it its own config
// directory, and the CLI loads its whole user layer from there. So a brand-new
// account starts with no slash commands, no skills, no subagents, no CLAUDE.md
// and no user-scope MCP servers — everything you built up in ~/.claude is
// invisible to it.
//
// The fix is to link, not copy. A symlink (or a junction on Windows) means
// there is one copy of the truth: edit a skill once and every account sees the
// edit. Credentials, sessions, transcripts and usage are never part of this and
// stay isolated per account, which is the whole point of separate accounts.

// LayerItem reports one shareable entry and whether it is currently linked.
type LayerItem struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Exists bool   `json:"exists"`
	IsDir  bool   `json:"isDir"`
	Linked bool   `json:"linked"`
	Note   string `json:"note,omitempty"`
}

// SourceLayer describes what was found in the user's own configuration, so the
// UI can show exactly what would be shared instead of asking for blind trust.
func (m *Manager) SourceLayer(p store.Provider) []LayerItem {
	src := SystemDir(p)
	out := make([]LayerItem, 0, len(claudefs.UserLayerEntries))
	for _, name := range claudefs.UserLayerEntries {
		full := filepath.Join(src, name)
		item := LayerItem{Name: name, Source: full}
		if st, err := os.Lstat(full); err == nil {
			item.Exists = true
			item.IsDir = st.IsDir()
		}
		out = append(out, item)
	}
	return out
}

// ApplyUserLayer links the user's own configuration into one account directory.
// It is idempotent: an existing correct link is left alone, and a real file that
// the user put there themselves is never overwritten.
func (m *Manager) ApplyUserLayer(a *store.Account) ([]LayerItem, error) {
	src := SystemDir(a.Provider)
	if a.Dir == "" {
		return nil, fmt.Errorf("account has no directory")
	}
	if sameDir(src, a.Dir) {
		// The system account is the source; there is nothing to link into it.
		return nil, nil
	}
	if err := os.MkdirAll(a.Dir, 0o700); err != nil {
		return nil, err
	}

	var out []LayerItem
	for _, name := range claudefs.UserLayerEntries {
		from := filepath.Join(src, name)
		to := filepath.Join(a.Dir, name)
		item := LayerItem{Name: name, Source: from}

		sfi, err := os.Lstat(from)
		if err != nil {
			out = append(out, item) // nothing to share
			continue
		}
		item.Exists = true
		item.IsDir = sfi.IsDir()

		// settings.json is per-account on purpose: it can carry account-scoped
		// state, so it is copied once if missing rather than linked.
		if name == "settings.json" {
			if _, err := os.Lstat(to); err != nil {
				if err := copyFile(from, to); err != nil {
					item.Note = "copy failed: " + err.Error()
				} else {
					item.Linked = true
					item.Note = "copied (per-account file)"
				}
			} else {
				item.Note = "already present, left alone"
			}
			out = append(out, item)
			continue
		}

		if dfi, err := os.Lstat(to); err == nil {
			if dfi.Mode()&os.ModeSymlink != 0 {
				if target, err := os.Readlink(to); err == nil && sameDir(target, from) {
					item.Linked = true
					item.Note = "already linked"
					out = append(out, item)
					continue
				}
				// A stale link to somewhere else: replace it.
				_ = os.Remove(to)
			} else {
				// Real content the user owns. Do not touch it.
				item.Note = "skipped: real file already there"
				out = append(out, item)
				continue
			}
		}

		if err := link(from, to, sfi.IsDir()); err != nil {
			item.Note = "link failed: " + err.Error()
		} else {
			item.Linked = true
		}
		out = append(out, item)
	}
	return out, nil
}

// RemoveUserLayer unlinks the shared entries from an account, leaving anything
// the account owns in place.
func (m *Manager) RemoveUserLayer(a *store.Account) error {
	if a.Dir == "" {
		return nil
	}
	src := SystemDir(a.Provider)
	if sameDir(src, a.Dir) {
		return nil
	}
	for _, name := range claudefs.UserLayerEntries {
		if name == "settings.json" {
			continue // it was a copy; it belongs to the account now
		}
		to := filepath.Join(a.Dir, name)
		fi, err := os.Lstat(to)
		if err != nil {
			continue
		}
		// Only ever remove links we could have made, never real content.
		if fi.Mode()&os.ModeSymlink == 0 && !isWindowsDirLink(to, fi) {
			continue
		}
		if err := os.Remove(to); err != nil {
			return err
		}
	}
	return nil
}

// SyncUserLayer applies or removes the shared layer across every account,
// following the current setting.
func (m *Manager) SyncUserLayer() error {
	share := m.st.Settings().ShareUserLayer
	for _, a := range m.st.Accounts() {
		var err error
		if share {
			_, err = m.ApplyUserLayer(a)
		} else {
			err = m.RemoveUserLayer(a)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", a.Name, err)
		}
	}
	return nil
}

// link creates a symlink, falling back to a directory junction on Windows where
// unprivileged symlink creation is often refused. If both fail for a directory
// we return the error rather than silently copying: a copy would drift, and
// silent drift in a config layer is worse than a visible failure.
func link(from, to string, isDir bool) error {
	if err := os.Symlink(from, to); err == nil {
		return nil
	} else if runtime.GOOS != "windows" {
		return err
	}
	if isDir {
		return junction(from, to)
	}
	// Windows without developer mode cannot symlink a file unprivileged; a hard
	// link keeps one copy of the bytes, which is the property we need.
	if err := os.Link(from, to); err == nil {
		return nil
	}
	return copyFile(from, to)
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(to, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
