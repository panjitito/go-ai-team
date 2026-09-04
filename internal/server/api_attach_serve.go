package server

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/uniair/go-ai-team/internal/claudefs"
	"github.com/uniair/go-ai-team/internal/session"
)

// Showing a pasted image back.
//
// Sending a screenshot puts its path into the prompt, because that is how the
// CLI is given a picture. But the conversation is then a message that reads as
// an absolute path — the person sent an image and gets back a filename, which is
// both ugly and useless for checking that the right thing was sent.
//
// So the server recognises its own paste paths in a message and turns them into
// image blocks the UI can render, and serves the bytes back on a route that can
// only ever reach that one directory.

// pasteRef matches a path inside the app's pastes directory, in either slash
// style, and captures the session folder and file name.
//
// Anchored on the two fixed segments rather than on a full path, because the
// message text is whatever the person typed around it and the CLI may echo it
// back with different separators.
var pasteRef = regexp.MustCompile(`(?i)[A-Za-z]:[\\/][^\s"']*?[\\/]pastes[\\/]([A-Za-z0-9_.-]+)[\\/]([A-Za-z0-9_.-]+\.(?:png|jpe?g|gif|webp))`)

// imageScaffold matches text that is nothing but the CLI's own attachment
// bookkeeping, left behind once the path is lifted out of it. An empty string
// counts too: a message that was only a picture has no words in it.
var imageScaffold = regexp.MustCompile(`^(\[Image:?\s*(source:)?\s*\]?)?$`)

// safeSegment is what a path segment must look like before it is joined onto the
// pastes root. Anything carrying a separator, a colon, a space or anything else
// is refused outright rather than cleaned, because "sanitise it and carry on" is
// how a traversal gets through.
//
// It must not begin with a dot, which is what rules out "." and ".." by
// construction rather than by remembering to check for them.
var safeSegment = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9_.-]*$`)

// servePastedFile returns one pasted image.
//
// The only inputs are two path segments, each of which must match a strict
// alphabet, and the result must still resolve inside the pastes root after
// symlinks are followed. This route can read nothing else on the machine.
func (s *Server) servePastedFile(w http.ResponseWriter, r *http.Request) {
	sess := r.PathValue("session")
	name := r.PathValue("name")
	if !safeSegment.MatchString(sess) || !safeSegment.MatchString(name) ||
		sess == "." || sess == ".." || name == "." || name == ".." {
		http.NotFound(w, r)
		return
	}

	root := session.AttachRoot(s.st.RootDir())
	path := filepath.Join(root, sess, name)

	// Belt and braces: resolve both sides and confirm the file really is under
	// the root. A segment that passed the alphabet check still must not escape.
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !strings.HasPrefix(real, realRoot+string(filepath.Separator)) {
		http.NotFound(w, r)
		return
	}

	f, err := os.Open(real)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}

	// These are the user's own pasted screenshots, immutable once written, so
	// they cache hard. Nothing here is shared or secret beyond the app itself.
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, real, st.ModTime(), f)
}

// withImageBlocks rewrites a message's text so pasted images render as pictures.
//
// The path is left in place as a caption rather than removed: it is what the
// agent was actually told, and hiding it would misrepresent the message.
func (s *Server) withImageBlocks(msgs []claudefs.Message) []claudefs.Message {
	for i := range msgs {
		var out []claudefs.Block
		changed := false
		for _, b := range msgs[i].Blocks {
			if b.Kind != claudefs.BlockText || b.Text == "" {
				out = append(out, b)
				continue
			}
			refs := pasteRef.FindAllStringSubmatch(b.Text, -1)
			if len(refs) == 0 {
				out = append(out, b)
				continue
			}
			changed = true

			// Whatever the person wrote around the paths stays as the message.
			//
			// Except the CLI's own scaffolding. Claude Code recognises a path in
			// a prompt and attaches the file itself, recording it as a second
			// user entry whose whole text is "[Image: source: <path>]". With the
			// path lifted out that leaves "[Image: source: ]" sitting above the
			// picture, which is a label for machinery the person never wrote.
			rest := strings.TrimSpace(pasteRef.ReplaceAllString(b.Text, ""))
			if !imageScaffold.MatchString(rest) {
				out = append(out, claudefs.Block{Kind: claudefs.BlockText, Text: rest})
			}
			for _, m := range refs {
				out = append(out, claudefs.Block{
					Kind: claudefs.BlockImage,
					Name: m[2],
					URL:  "/api/pastes/" + m[1] + "/" + m[2],
				})
			}
		}
		if changed {
			msgs[i].Blocks = out
		}
	}
	return msgs
}
