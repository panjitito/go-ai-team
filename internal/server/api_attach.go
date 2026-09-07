package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/panjitito/go-ai-team/internal/session"
	"github.com/panjitito/go-ai-team/internal/store"
)

// Pasting an image into the chat.
//
// People paste screenshots constantly, and until now the composer threw them
// away: a textarea ignores image data, so the picture vanished with no error and
// Send did nothing at all.
//
// The image cannot simply be handed to the CLI. Claude Code reads the clipboard
// of the machine it runs on, and that is not necessarily the machine holding the
// picture — the whole point of the browser UI is that it also opens from a
// phone. What does work everywhere is a file: the image is written to disk next
// to the agent, and its path goes into the prompt, so the agent reads it with
// the same tool it uses for any other file.
//
// The files live outside the project so pasting a screenshot never leaves
// anything in somebody's repository to be committed by accident. Sessions are
// launched with that directory added to the ones the CLI may read, which is what
// keeps a paste from turning into a permission prompt every single time.

// maxAttachBytes caps an upload. Large enough for a full-resolution screenshot,
// small enough that a stray video file is refused rather than copied.
const maxAttachBytes = 12 << 20

// attachResp tells the UI where the file landed, so it can put the path in the
// prompt and show a thumbnail of what is about to be sent.
type attachResp struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Type string `json:"type"`
	Size int    `json:"size"`
}

// attachToSession stores one pasted or dropped image for a session.
func (s *Server) attachToSession(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.sm.Get(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, store.ErrNotFound)
		return
	}

	body, err := readLimited(r, maxAttachBytes)
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, fmt.Errorf(
			"that image is larger than %d MB. Save it to a file and mention the path instead",
			maxAttachBytes>>20))
		return
	}
	if len(body) == 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("the upload was empty"))
		return
	}

	// The content type is taken from the bytes, never from the request. A header
	// is whatever the caller chose to say, and this decides a file extension.
	kind, ext, err := imageKind(body)
	if err != nil {
		writeErr(w, http.StatusUnsupportedMediaType, err)
		return
	}

	dir := session.AttachDir(s.st.RootDir(), sess.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	name := attachName(r.URL.Query().Get("name"), ext)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, attachResp{
		// Forward slashes read correctly to the CLI on every platform, and are
		// what a person would type.
		Path: filepath.ToSlash(path),
		Name: name,
		Type: kind,
		Size: len(body),
	})
}

// imageKind identifies an image from its leading bytes and returns the type and
// the extension to save it under.
//
// Only formats a model can actually look at are accepted. Refusing anything else
// here is what stops "paste an image" from becoming "write arbitrary files into
// a directory the agent is allowed to read".
func imageKind(b []byte) (kind, ext string, err error) {
	switch {
	case len(b) > 8 && string(b[:8]) == "\x89PNG\r\n\x1a\n":
		return "image/png", ".png", nil
	case len(b) > 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
		return "image/jpeg", ".jpg", nil
	case len(b) > 6 && (string(b[:6]) == "GIF87a" || string(b[:6]) == "GIF89a"):
		return "image/gif", ".gif", nil
	case len(b) > 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "image/webp", ".webp", nil
	}
	return "", "", fmt.Errorf("that does not look like a PNG, JPEG, GIF or WebP image")
}

// attachName builds a safe, unique filename, keeping something recognisable from
// what the browser called it.
func attachName(given, ext string) string {
	// baseName, not filepath.Base: this name came from a browser and carries
	// that machine's separators, which are not necessarily this one's.
	// filepath.Ext is safe once the separators are gone, because there is
	// nothing left for the two platforms to disagree about.
	base := baseName(strings.TrimSpace(given))
	base = strings.TrimSuffix(base, filepath.Ext(base))
	var keep []rune
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			keep = append(keep, r)
		}
		if len(keep) >= 24 {
			break
		}
	}
	if len(keep) == 0 {
		keep = []rune("pasted")
	}
	return fmt.Sprintf("%s-%s%s", time.Now().Format("150405"), string(keep), ext)
}
