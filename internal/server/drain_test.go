package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A handler that ignores the request body must not cost the client its
// response.
//
// Several handlers here take an id in the path and read nothing, and the UI
// posts `{}` at them anyway. Leaving those bytes unread means the server closes
// the socket with data still in flight, and Windows turns that into a reset
// rather than a graceful close — the response is written, sent, and then thrown
// away by the transport.
//
// It looks like a rare unexplained failure, and it gets commoner the bigger the
// body: measured on POST /api/prompts/{id}/resolve, no body succeeded 15 times
// out of 15, `{}` 14 of 15, and a 4KB body only 9 of 15.
func TestDrainRequestBody(t *testing.T) {
	// A handler that writes a reply and never touches r.Body.
	ignores := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	h := drainRequestBody(ignores)

	for _, size := range []int{0, 2, 4000, 200000} {
		body := strings.Repeat("x", size)
		req := httptest.NewRequest("POST", "/api/anything", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("size %d: status %d", size, rec.Code)
		}
		// The body must have been consumed, which is what stops the reset.
		left, _ := io.ReadAll(req.Body)
		if len(left) != 0 {
			t.Errorf("size %d: %d bytes left unread", size, len(left))
		}
	}
}

// A handler that does read the body still gets all of it, and draining after
// the fact changes nothing.
func TestDrainLeavesReadingHandlersAlone(t *testing.T) {
	var got []byte
	reads := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	})
	h := drainRequestBody(reads)

	want := strings.Repeat("payload ", 500)
	req := httptest.NewRequest("POST", "/api/anything", strings.NewReader(want))
	h.ServeHTTP(httptest.NewRecorder(), req)

	if string(got) != want {
		t.Errorf("handler saw %d bytes, sent %d", len(got), len(want))
	}
}

// A websocket upgrade hands the socket to the other side. Reading the body after
// that is reaching into a connection this code no longer owns.
func TestDrainSkipsUpgrades(t *testing.T) {
	touched := false
	h := drainRequestBody(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Stand in for a hijack: replace the body with one that records a read.
		r.Body = io.NopCloser(readSpy{&touched})
		w.WriteHeader(http.StatusSwitchingProtocols)
	}))

	for _, tc := range []struct {
		name string
		path string
		hdr  string
	}{
		{"by path", "/ws/pty", ""},
		{"by header", "/api/whatever", "websocket"},
		{"header case", "/api/whatever", "WebSocket"},
	} {
		touched = false
		req := httptest.NewRequest("GET", tc.path, bytes.NewReader([]byte("data")))
		if tc.hdr != "" {
			req.Header.Set("Upgrade", tc.hdr)
		}
		h.ServeHTTP(httptest.NewRecorder(), req)
		if touched {
			t.Errorf("%s: the body was read after the connection was handed over", tc.name)
		}
	}

	// An ordinary request is still drained.
	touched = false
	req := httptest.NewRequest("POST", "/api/whatever", bytes.NewReader([]byte("data")))
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !touched {
		t.Error("an ordinary request was not drained")
	}
}

type readSpy struct{ hit *bool }

func (s readSpy) Read(p []byte) (int, error) {
	*s.hit = true
	return 0, io.EOF
}
