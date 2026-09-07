package automation

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/panjitito/go-ai-team/internal/store"
)

// Webhook triggers.
//
// The shape of the problem: a public URL that starts a coding agent is an
// obvious way to get somebody else's machine to run something. So a delivery
// has to pass four gates before an agent starts — a signature, an optional
// filter, a burst limit, and an enabled trigger — and a delivery that fails any
// of them is recorded rather than silently dropped, because "my webhook does
// nothing" is otherwise unanswerable.

// Hooks fires agents from incoming deliveries.
type Hooks struct {
	st *store.Store
	l  Launcher
	n  Notifier

	mu sync.Mutex
	// runs tracks recent launch times per trigger for the burst limit.
	runs map[string][]time.Time
}

// NewHooks builds a Hooks.
func NewHooks(st *store.Store, l Launcher, n Notifier) *Hooks {
	return &Hooks{st: st, l: l, n: n, runs: map[string][]time.Time{}}
}

// Result describes what happened to one delivery.
type Result struct {
	Accepted  bool   `json:"accepted"`
	Reason    string `json:"reason"`
	SessionID string `json:"sessionId,omitempty"`
	Queued    bool   `json:"queued,omitempty"`
}

// Deliver processes an incoming payload for one trigger.
func (h *Hooks) Deliver(w *store.Webhook, body []byte, headers map[string]string) Result {
	_, _ = h.st.UpdateWebhook(w.ID, func(x *store.Webhook) {
		x.Hits++
		x.LastHit = time.Now()
	})

	if !w.Enabled {
		return h.reject(w, "trigger is disabled")
	}

	// 1. Signature. A trigger with a secret must be signed; that is the whole
	// point of having one.
	if w.Secret != "" {
		if err := verify(w.Secret, body, headers); err != nil {
			return h.reject(w, "signature check failed: "+err.Error())
		}
	}

	// 2. Payload becomes prompt variables.
	vars := flatten(body, headers)

	// 3. Filter.
	if strings.TrimSpace(w.Filter) != "" {
		ok, err := Match(w.Filter, vars)
		if err != nil {
			return h.reject(w, "filter could not be evaluated: "+err.Error())
		}
		if !ok {
			// Not an error: most deliveries are meant to be ignored. Recording
			// it without an error keeps the trigger's status clean.
			return Result{Reason: "filter did not match, nothing run"}
		}
	}

	// 4. Burst limit, so a noisy service cannot start twenty agents.
	if !h.allow(w) {
		return h.reject(w, fmt.Sprintf("burst limit of %d runs per hour reached", h.limitOf(w)))
	}

	prompt := Interpolate(w.Prompt, vars)
	sid, err := h.l.LaunchTask(w.ProjectID, w.AgentID, prompt,
		fmt.Sprintf("webhook %q", w.Name))
	if err != nil {
		// A launch failure while nothing can run is exactly the case worth
		// queueing: the event is real and replaying it later is correct.
		_ = h.st.AddQueuedEvent(&store.QueuedEvent{WebhookID: w.ID, Vars: vars})
		h.recordErr(w, err.Error())
		return Result{Reason: "could not start an agent, delivery queued for the next launch: " + err.Error(), Queued: true}
	}

	_, _ = h.st.UpdateWebhook(w.ID, func(x *store.Webhook) {
		x.Runs++
		x.LastErr = ""
	})
	h.n.Notify("webhook.ran", fmt.Sprintf("Webhook %q started an agent", w.Name),
		map[string]string{"webhookId": w.ID, "sessionId": sid})
	return Result{Accepted: true, Reason: "agent started", SessionID: sid}
}

func (h *Hooks) reject(w *store.Webhook, why string) Result {
	h.recordErr(w, why)
	return Result{Reason: why}
}

func (h *Hooks) recordErr(w *store.Webhook, msg string) {
	_, _ = h.st.UpdateWebhook(w.ID, func(x *store.Webhook) { x.LastErr = msg })
}

func (h *Hooks) limitOf(w *store.Webhook) int {
	if w.BurstLimit > 0 {
		return w.BurstLimit
	}
	return 10
}

// allow implements a rolling one-hour window per trigger.
func (h *Hooks) allow(w *store.Webhook) bool {
	limit := h.limitOf(w)
	h.mu.Lock()
	defer h.mu.Unlock()
	cutoff := time.Now().Add(-time.Hour)
	kept := h.runs[w.ID][:0]
	for _, t := range h.runs[w.ID] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	h.runs[w.ID] = kept
	if len(kept) >= limit {
		return false
	}
	h.runs[w.ID] = append(h.runs[w.ID], time.Now())
	return true
}

// ReplayQueued runs deliveries that arrived while the app was closed. Only one
// machine should take each one, which is why the event is deleted before the
// launch rather than after.
func (h *Hooks) ReplayQueued() {
	for _, e := range h.st.QueuedEvents() {
		w, err := h.st.Webhook(e.WebhookID)
		if err != nil || !w.Enabled {
			_ = h.st.DeleteQueuedEvent(e.ID)
			continue
		}
		if err := h.st.DeleteQueuedEvent(e.ID); err != nil {
			continue
		}
		prompt := Interpolate(w.Prompt, e.Vars)
		sid, err := h.l.LaunchTask(w.ProjectID, w.AgentID, prompt,
			fmt.Sprintf("webhook %q (replayed from %s)", w.Name, e.Received.Format("15:04")))
		if err != nil {
			h.recordErr(w, "replay failed: "+err.Error())
			continue
		}
		_, _ = h.st.UpdateWebhook(w.ID, func(x *store.Webhook) { x.Runs++ })
		h.n.Notify("webhook.replayed",
			fmt.Sprintf("Replayed a queued delivery for %q", w.Name),
			map[string]string{"webhookId": w.ID, "sessionId": sid})
	}
}

// verify checks the signature headers the common services send. Each is a
// keyed HMAC of the raw body, so the body must be the exact bytes received —
// re-serialising the JSON first would break every one of them.
func verify(secret string, body []byte, headers map[string]string) error {
	get := func(k string) string { return headers[strings.ToLower(k)] }

	// GitHub: sha256=<hex>, and the older sha1 form.
	if sig := get("X-Hub-Signature-256"); sig != "" {
		return compare(hmacHex(sha256.New, secret, body), strings.TrimPrefix(sig, "sha256="))
	}
	if sig := get("X-Hub-Signature"); sig != "" {
		return compare(hmacHex(sha1.New, secret, body), strings.TrimPrefix(sig, "sha1="))
	}
	// GitLab and Linear send a shared token rather than an HMAC.
	if tok := get("X-Gitlab-Token"); tok != "" {
		return compare(secret, tok)
	}
	// Generic: our own header, plain HMAC-SHA256 hex.
	if sig := get("X-Go-AI-Team-Signature"); sig != "" {
		return compare(hmacHex(sha256.New, secret, body), strings.TrimPrefix(sig, "sha256="))
	}
	return fmt.Errorf("this trigger has a secret but the delivery carried no signature header")
}

// hmacHex computes a keyed digest of the raw body. The body must be the exact
// bytes received: re-serialising the JSON first changes whitespace and key
// order, and every one of these signatures would then fail.
func hmacHex(newHash func() hash.Hash, secret string, body []byte) string {
	m := hmac.New(newHash, []byte(secret))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

func compare(want, got string) error {
	if subtle.ConstantTimeCompare([]byte(strings.ToLower(want)), []byte(strings.ToLower(strings.TrimSpace(got)))) == 1 {
		return nil
	}
	return fmt.Errorf("signature did not match")
}

// flatten turns a JSON payload into flat prompt variables: event.title,
// event.pull_request.number, and so on. A nested payload is far easier to
// reference in a prompt this way than with any query syntax.
func flatten(body []byte, headers map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range headers {
		out["header."+k] = v
	}
	out["event.raw"] = truncate(string(body), 4000)

	var v any
	if json.Unmarshal(body, &v) != nil {
		return out
	}
	walk("event", v, out, 0)
	return out
}

func walk(prefix string, v any, out map[string]string, depth int) {
	if depth > 6 || len(out) > 400 {
		return
	}
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			walk(prefix+"."+k, val, out, depth+1)
		}
	case []any:
		out[prefix+".count"] = strconv.Itoa(len(t))
		for i, val := range t {
			if i >= 10 {
				break
			}
			walk(prefix+"."+strconv.Itoa(i), val, out, depth+1)
		}
	case string:
		out[prefix] = truncate(t, 2000)
	case float64:
		if t == float64(int64(t)) {
			out[prefix] = strconv.FormatInt(int64(t), 10)
		} else {
			out[prefix] = strconv.FormatFloat(t, 'f', -1, 64)
		}
	case bool:
		out[prefix] = strconv.FormatBool(t)
	case nil:
		out[prefix] = ""
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// Interpolate substitutes {{name}} references from the payload variables. An
// unknown reference is left visible in the prompt rather than blanked, so a
// mistyped variable is obvious in the agent's first message instead of silently
// producing a nonsense instruction.
func Interpolate(tmpl string, vars map[string]string) string {
	if !strings.Contains(tmpl, "{{") {
		return tmpl
	}
	var b strings.Builder
	rest := tmpl
	for {
		i := strings.Index(rest, "{{")
		if i < 0 {
			b.WriteString(rest)
			break
		}
		b.WriteString(rest[:i])
		rest = rest[i+2:]
		j := strings.Index(rest, "}}")
		if j < 0 {
			b.WriteString("{{")
			b.WriteString(rest)
			break
		}
		name := strings.TrimSpace(rest[:j])
		rest = rest[j+2:]
		if val, ok := vars[name]; ok {
			b.WriteString(val)
		} else {
			b.WriteString("{{" + name + "}}")
		}
	}
	return b.String()
}

// Match evaluates a filter expression against the payload variables.
//
// Deliberately tiny: one comparison, optionally joined by && or ||. A full
// expression language here would be a security surface for something that only
// ever needs to answer "is this the event I care about".
//
// Supported: name == "value", name != "value", name contains "value",
// name exists, plus && and || between them.
func Match(expr string, vars map[string]string) (bool, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return true, nil
	}
	// || binds loosest, so split on it first.
	if parts := splitTop(expr, "||"); len(parts) > 1 {
		for _, p := range parts {
			ok, err := Match(p, vars)
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
		return false, nil
	}
	if parts := splitTop(expr, "&&"); len(parts) > 1 {
		for _, p := range parts {
			ok, err := Match(p, vars)
			if err != nil {
				return false, err
			}
			if !ok {
				return false, nil
			}
		}
		return true, nil
	}
	return matchOne(expr, vars)
}

// splitTop splits on a separator that is not inside quotes.
func splitTop(s, sep string) []string {
	var out []string
	depth := 0
	inStr := false
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' {
			inStr = !inStr
			continue
		}
		if inStr {
			continue
		}
		if c == '(' {
			depth++
		}
		if c == ')' {
			depth--
		}
		if depth == 0 && strings.HasPrefix(s[i:], sep) {
			out = append(out, s[start:i])
			i += len(sep) - 1
			start = i + 1
		}
	}
	out = append(out, s[start:])
	if len(out) == 1 {
		return out
	}
	return out
}

func matchOne(expr string, vars map[string]string) (bool, error) {
	expr = strings.TrimSpace(strings.Trim(strings.TrimSpace(expr), "()"))
	unquote := func(s string) string {
		s = strings.TrimSpace(s)
		if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
			return s[1 : len(s)-1]
		}
		return s
	}

	for _, op := range []string{"==", "!=", " contains ", " startswith ", " endswith "} {
		if i := strings.Index(expr, op); i > 0 {
			name := strings.TrimSpace(expr[:i])
			want := unquote(expr[i+len(op):])
			got, present := vars[name]
			switch strings.TrimSpace(op) {
			case "==":
				return present && got == want, nil
			case "!=":
				return got != want, nil
			case "contains":
				return present && strings.Contains(got, want), nil
			case "startswith":
				return present && strings.HasPrefix(got, want), nil
			case "endswith":
				return present && strings.HasSuffix(got, want), nil
			}
		}
	}
	if strings.HasSuffix(expr, " exists") {
		name := strings.TrimSpace(strings.TrimSuffix(expr, " exists"))
		v, ok := vars[name]
		return ok && v != "", nil
	}
	return false, fmt.Errorf("cannot read the filter %q; expected something like: event.action == \"opened\"", expr)
}

// SignHex produces the HMAC-SHA256 hex digest a GitHub-style signature header
// carries. Exported so the "send a test delivery" action can sign its own
// payload and exercise the real verification path rather than bypassing it.
func SignHex(secret string, body []byte) string {
	return hmacHex(sha256.New, secret, body)
}
