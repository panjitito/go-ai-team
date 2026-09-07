package server

import (
	"encoding/json"
	"log"
	"net/http"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/panjitito/go-ai-team/internal/session"
)

// lookPath is indirected so tests can stub it.
var lookPath = exec.LookPath

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 32 * 1024,
	// Same-origin only. The UI is served from this very server, so a foreign
	// page must never be able to open a socket onto a live terminal.
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true // non-browser client
		}
		host := r.Host
		for _, prefix := range []string{"http://", "https://"} {
			if origin == prefix+host {
				return true
			}
		}
		return false
	},
}

// safeConn serialises websocket writes. gorilla allows only one concurrent
// writer, and here the PTY pump, the ping ticker and error replies all write.
type safeConn struct {
	mu sync.Mutex
	c  *websocket.Conn
}

func (s *safeConn) writeMessage(t int, b []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return s.c.WriteMessage(t, b)
}

func (s *safeConn) writeJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.writeMessage(websocket.TextMessage, b)
}

// ptyClientMsg is what the browser sends on the terminal socket.
type ptyClientMsg struct {
	Type string `json:"type"` // "input" | "resize" | "ping"
	Data string `json:"data,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
}

// wsPTY bridges one browser terminal to one session.
//
// Terminal bytes go over the socket as binary frames and control messages as
// JSON text frames, so output never has to be base64-encoded or escaped: the
// bytes the CLI wrote are the bytes xterm.js renders.
func (s *Server) wsPTY(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("session")
	if id == "" {
		http.Error(w, "session query parameter is required", http.StatusBadRequest)
		return
	}
	scrollback, out, cancel, err := s.sm.Attach(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	raw, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		cancel()
		return
	}
	conn := &safeConn{c: raw}
	defer func() {
		cancel()
		_ = raw.Close()
	}()

	// Replay the scrollback so an attach mid-run is never a blank screen.
	if len(scrollback) > 0 {
		if err := conn.writeMessage(websocket.BinaryMessage, scrollback); err != nil {
			return
		}
	}
	if sess, ok := s.sm.Get(id); ok {
		_ = conn.writeJSON(map[string]any{"type": "session", "session": sess.Public()})
	}

	// Apply the client's initial size if it told us one up front.
	if c, _ := strconv.Atoi(r.URL.Query().Get("cols")); c > 0 {
		rw, _ := strconv.Atoi(r.URL.Query().Get("rows"))
		if rw > 0 {
			_ = s.sm.Resize(id, uint16(c), uint16(rw))
		}
	}

	done := make(chan struct{})

	// Reader: browser -> PTY.
	go func() {
		defer close(done)
		raw.SetReadLimit(1 << 20)
		for {
			mt, data, err := raw.ReadMessage()
			if err != nil {
				return
			}
			switch mt {
			case websocket.BinaryMessage:
				if err := s.sm.Write(id, data); err != nil {
					return
				}
			case websocket.TextMessage:
				var msg ptyClientMsg
				if json.Unmarshal(data, &msg) != nil {
					continue
				}
				switch msg.Type {
				case "input":
					if err := s.sm.Write(id, []byte(msg.Data)); err != nil {
						return
					}
				case "resize":
					_ = s.sm.Resize(id, msg.Cols, msg.Rows)
				}
			}
		}
	}()

	// Writer: PTY -> browser, plus a keepalive so an idle agent's socket is not
	// dropped by an intermediary while the user is away.
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case chunk, ok := <-out:
			if !ok {
				return
			}
			if err := conn.writeMessage(websocket.BinaryMessage, chunk); err != nil {
				return
			}
		case <-ping.C:
			if err := conn.writeMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-done:
			return
		}
	}
}

// wsEvents streams state changes: status flips, token updates, account benching
// and the account hand-off. It is what keeps every open tab — desktop and phone
// — showing the same thing without polling.
func (s *Server) wsEvents(w http.ResponseWriter, r *http.Request) {
	raw, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn := &safeConn{c: raw}
	defer raw.Close()

	events, cancel := s.sm.Subscribe()
	defer cancel()

	// Drain client frames so pongs and closes are processed.
	go func() {
		raw.SetReadLimit(4096)
		for {
			if _, _, err := raw.ReadMessage(); err != nil {
				return
			}
		}
	}()

	// An immediate snapshot means a fresh tab renders correctly before the first
	// event ever fires.
	sessions := s.sm.Sessions()
	snap := make([]session.PublicSession, 0, len(sessions))
	for _, sess := range sessions {
		snap = append(snap, sess.Public())
	}
	if err := conn.writeJSON(map[string]any{
		"type": "snapshot", "sessions": snap, "accounts": s.accs.Statuses(),
	}); err != nil {
		return
	}

	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case e, ok := <-events:
			if !ok {
				return
			}
			if err := conn.writeJSON(e); err != nil {
				return
			}
		case <-ping.C:
			if err := conn.writeMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func init() {
	// gorilla logs handshake failures to the default logger; keep them quiet
	// unless something genuinely broke.
	log.SetFlags(log.Ltime)
}
