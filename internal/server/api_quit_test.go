package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The way out, now that Ctrl-C is not one.
//
// The console is given back on startup so no black box sits behind the window,
// and that took the interrupt with it. In a browser tab there is no window to
// close and no icon in the notification area either, so this endpoint is the
// only way to stop the app — which makes "it answers, and then it stops"
// something worth pinning rather than assuming.
func TestQuitAnswersThenStops(t *testing.T) {
	s := &Server{}
	stopped := make(chan struct{})
	s.OnQuit(func() { close(stopped) })

	w := httptest.NewRecorder()
	s.quit(w, httptest.NewRequest(http.MethodPost, "/api/quit", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var got map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unreadable: %v (%s)", err, w.Body.String())
	}
	if got["status"] != "stopping" {
		t.Errorf("said %q", got["status"])
	}

	// Answered first, stopped second. Shutting down inside the handler closes
	// the listener under the reply, and the page then reports a network error
	// for something that worked perfectly.
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("the app was never asked to stop")
	}
}

// A build with nothing wired to it says so rather than pretending.
func TestQuitWithNothingToStop(t *testing.T) {
	s := &Server{}
	w := httptest.NewRecorder()
	s.quit(w, httptest.NewRequest(http.MethodPost, "/api/quit", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var got map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got["status"] == "stopping" {
		t.Error("claimed to be stopping with nothing wired up to stop it")
	}
	if got["status"] == "" {
		t.Error("said nothing at all")
	}
}

// Two clicks must not start two shutdowns. The guard lives in main, so what is
// checked here is that the endpoint hands every request to the same callback
// and lets that callback be the one to decide.
func TestQuitIsIdempotentThroughItsCallback(t *testing.T) {
	s := &Server{}
	calls := make(chan struct{}, 4)
	s.OnQuit(func() { calls <- struct{}{} })

	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		s.quit(w, httptest.NewRequest(http.MethodPost, "/api/quit", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: status %d", i, w.Code)
		}
	}

	deadline := time.After(3 * time.Second)
	for i := 0; i < 3; i++ {
		select {
		case <-calls:
		case <-deadline:
			t.Fatalf("only %d of 3 requests reached the callback", i)
		}
	}
}
