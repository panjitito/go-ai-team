package server

import (
	"net/http"
	"strconv"

	"github.com/uniair/go-ai-team/internal/claudefs"
	"github.com/uniair/go-ai-team/internal/session"
	"github.com/uniair/go-ai-team/internal/store"
)

// The conversation endpoint.
//
// This is what makes the UI a chat rather than a screen scrape: it returns the
// structured turns from the transcript the CLI already writes, plus whether the
// CLI is currently asking something that only the terminal can answer.

type conversationResp struct {
	Messages []claudefs.Message `json:"messages"`
	// SessionID is the CLI's own session id, empty until it writes one.
	SessionID string `json:"sessionId,omitempty"`
	// Ready is false before a transcript exists, so the UI can say "starting"
	// rather than "no messages".
	Ready bool `json:"ready"`

	// NeedsTerminal is true when the CLI is drawing a prompt that must be
	// answered with the keyboard in the terminal view.
	NeedsTerminal bool   `json:"needsTerminal"`
	PromptTitle   string `json:"promptTitle,omitempty"`
	PromptText    string `json:"promptText,omitempty"`

	Status session.Status `json:"status"`

	// The token meter, carried on the poll the chat view already makes so the
	// header badge does not depend on a separate list refresh landing first.
	TotalTokens  int64   `json:"totalTokens"`
	CacheHitRate float64 `json:"cacheHitRate"`
}

func (s *Server) conversation(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.sm.Get(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, store.ErrNotFound)
		return
	}
	p := sess.Public()

	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}

	out := conversationResp{
		Messages:     []claudefs.Message{},
		SessionID:    p.ClaudeSessionID,
		Status:       p.Status,
		TotalTokens:  p.TotalTokens,
		CacheHitRate: p.CacheHitRate,
	}

	// A prompt in the terminal is checked first: it is the case where the
	// conversation view would otherwise look frozen for no visible reason.
	if need, title := session.NeedsTerminal(s.sm.Tail(p.ID, 6000)); need {
		out.NeedsTerminal = true
		out.PromptTitle = title
		out.PromptText = session.PromptTail(s.sm.Tail(p.ID, 6000), 14)
	}

	if p.ClaudeSessionID != "" {
		if path, found := claudefs.FindTranscript(p.AccountDir, p.CWD, p.ClaudeSessionID); found {
			if msgs, err := claudefs.ParseConversation(path, limit); err == nil {
				out.Messages = s.withImageBlocks(msgs)
				out.Ready = true
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}
