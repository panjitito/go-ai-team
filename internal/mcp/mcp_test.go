package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/panjitito/go-ai-team/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	st, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// exchange runs a batch of JSON-RPC lines through the server and returns the
// decoded responses, which is the only honest way to test a stdio protocol.
func exchange(t *testing.T, s *Server, lines ...string) []map[string]any {
	t.Helper()
	in := strings.NewReader(strings.Join(lines, "\n") + "\n")
	var out bytes.Buffer
	if err := s.Serve(context.Background(), in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var res []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("response was not JSON: %q", l)
		}
		res = append(res, m)
	}
	return res
}

func TestProtocolBasics(t *testing.T) {
	st := newTestStore(t)
	s := NewServer("test", "1.0")
	Register(s, Deps{Store: st, ProjectID: "prj_1", AgentName: "Tester"})

	res := exchange(t,
		s,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"ping"}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`, // no id: no reply
		`{"jsonrpc":"2.0","id":4,"method":"no/such/method"}`,
		`not json at all`,
	)

	// The notification must produce no response, so ids 1,2,3,4 plus one parse
	// error is five.
	if len(res) != 5 {
		t.Fatalf("got %d responses, want 5: %v", len(res), res)
	}

	init := res[0]["result"].(map[string]any)
	if init["protocolVersion"] != protocolVersion {
		t.Errorf("protocolVersion = %v", init["protocolVersion"])
	}

	tools := res[1]["result"].(map[string]any)["tools"].([]any)
	if len(tools) < 10 {
		t.Errorf("only %d tools registered", len(tools))
	}
	// Every tool needs a name, a description and a schema, or an agent cannot
	// decide whether to call it.
	for _, raw := range tools {
		tt := raw.(map[string]any)
		for _, k := range []string{"name", "description", "inputSchema"} {
			if tt[k] == nil || tt[k] == "" {
				t.Errorf("tool %v is missing %s", tt["name"], k)
			}
		}
	}

	if res[4]["error"] == nil {
		t.Error("an unknown method should be a JSON-RPC error")
	}
}

// A tool that cannot do its job must return isError, not a protocol fault: the
// agent should be able to read the reason and adapt.
func TestToolFailureIsData(t *testing.T) {
	st := newTestStore(t)
	s := NewServer("test", "1.0")
	Register(s, Deps{Store: st, ProjectID: "prj_1", AgentName: "Tester"})

	res := exchange(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"backlog_create","arguments":{"title":"  "}}}`)
	r, ok := res[0]["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected a result, got %v", res[0])
	}
	if r["isError"] != true {
		t.Errorf("expected isError, got %v", r)
	}

	// A tool that does not exist is a protocol error, because the agent asked
	// for something that is not on the menu.
	res = exchange(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"nope","arguments":{}}}`)
	if res[0]["error"] == nil {
		t.Error("an unknown tool should be a JSON-RPC error")
	}
}

// The project scope is the whole trust boundary of this process: an agent must
// not be able to read or write another project's data.
func TestProjectScopeIsEnforced(t *testing.T) {
	st := newTestStore(t)
	_ = st.AddProject(&store.Project{ID: "prj_mine", Path: `C:\a`})
	_ = st.AddProject(&store.Project{ID: "prj_theirs", Path: `C:\b`})
	_ = st.AddAgent(&store.Agent{ID: "agt_theirs", ProjectID: "prj_theirs", Name: "Other"})

	theirTask := &store.Task{ProjectID: "prj_theirs", Title: "Not yours", Status: store.TaskBacklog}
	_ = st.AddTask(theirTask)
	_ = st.AddMemory(&store.Memory{ProjectID: "prj_theirs", Kind: store.MemDecision,
		Title: "Their decision", Body: "secret"})

	s := NewServer("test", "1.0")
	Register(s, Deps{Store: st, ProjectID: "prj_mine", AgentID: "agt_mine", AgentName: "Mine"})

	// The other project's task must not be listed.
	res := exchange(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"backlog_list","arguments":{}}}`)
	text := resultText(res[0])
	if strings.Contains(text, "Not yours") {
		t.Error("backlog_list leaked another project's task")
	}

	// Nor updated, even with its exact id.
	res = exchange(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"backlog_update","arguments":{"id":"`+theirTask.ID+`","status":"done"}}}`)
	r := res[0]["result"].(map[string]any)
	if r["isError"] != true {
		t.Error("backlog_update accepted a task from another project")
	}
	if got, _ := st.Task(theirTask.ID); got.Status == store.TaskDone {
		t.Error("another project's task was actually modified")
	}

	// Memory is scoped the same way.
	res = exchange(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"memory_read","arguments":{}}}`)
	if strings.Contains(resultText(res[0]), "Their decision") {
		t.Error("memory_read leaked another project's memory")
	}

	// And so is messaging.
	res = exchange(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"agent_send","arguments":{"to":"agt_theirs","subject":"hi","body":"x"}}}`)
	if res[0]["result"].(map[string]any)["isError"] != true {
		t.Error("agent_send reached an agent on another project")
	}
}

func resultText(m map[string]any) string {
	r, ok := m["result"].(map[string]any)
	if !ok {
		return ""
	}
	c, ok := r["content"].([]any)
	if !ok || len(c) == 0 {
		return ""
	}
	b, _ := c[0].(map[string]any)
	s, _ := b["text"].(string)
	return s
}

func TestBacklogRoundTrip(t *testing.T) {
	st := newTestStore(t)
	_ = st.AddProject(&store.Project{ID: "prj_1", Path: `C:\a`})
	s := NewServer("test", "1.0")
	Register(s, Deps{Store: st, ProjectID: "prj_1", AgentName: "Tester"})

	res := exchange(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"backlog_create","arguments":{"title":"Do the thing","body":"detail"}}}`)
	if !strings.Contains(resultText(res[0]), "Created task") {
		t.Fatalf("create failed: %s", resultText(res[0]))
	}
	tasks := st.TasksFor("prj_1")
	if len(tasks) != 1 || tasks[0].Title != "Do the thing" {
		t.Fatalf("task not stored: %+v", tasks)
	}
	// The agent's name should be recorded, so the board says where it came from.
	if tasks[0].Source != "agent" || tasks[0].Reporter != "Tester" {
		t.Errorf("attribution missing: source=%q reporter=%q", tasks[0].Source, tasks[0].Reporter)
	}

	// A comment through backlog_update lands on the thread.
	res = exchange(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"backlog_update","arguments":{"id":"`+tasks[0].ID+`","status":"review","comment":"changed three files"}}}`)
	if !strings.Contains(resultText(res[0]), "review") {
		t.Errorf("update reply = %q", resultText(res[0]))
	}
	got, _ := st.Task(tasks[0].ID)
	if got.Status != store.TaskReview {
		t.Errorf("status = %v", got.Status)
	}
	if len(got.Comments) != 1 || !got.Comments[0].IsAgent {
		t.Errorf("agent comment not recorded: %+v", got.Comments)
	}
}

func TestResolveChain(t *testing.T) {
	st := newTestStore(t)
	_ = st.AddPrompt(&store.Prompt{Name: "stack", Body: "Go and vanilla JS."})
	_ = st.AddPrompt(&store.Prompt{Name: "rules", Body: "Stack: {{prompt:stack}} Keep it small."})

	got := ResolveChain(st, "Brief: {{prompt:rules}}", 0)
	if !strings.Contains(got, "Go and vanilla JS.") {
		t.Errorf("nested reference not expanded: %q", got)
	}
	if strings.Contains(got, "{{prompt:") {
		t.Errorf("a reference was left unexpanded: %q", got)
	}

	// An unknown name stays visible, so a typo is obvious rather than silently
	// producing a brief with a hole in it.
	got = ResolveChain(st, "x {{prompt:nope}} y", 0)
	if got != "x {{prompt:nope}} y" {
		t.Errorf("unknown reference = %q", got)
	}

	// A cycle must terminate rather than hang.
	_ = st.AddPrompt(&store.Prompt{Name: "a", Body: "A{{prompt:b}}"})
	_ = st.AddPrompt(&store.Prompt{Name: "b", Body: "B{{prompt:a}}"})
	done := make(chan string, 1)
	go func() { done <- ResolveChain(st, "{{prompt:a}}", 0) }()
	select {
	case out := <-done:
		if !strings.HasPrefix(out, "AB") {
			t.Errorf("cycle output looks wrong: %q", out)
		}
	case <-timeout():
		t.Fatal("ResolveChain did not terminate on a cycle")
	}

	// An unterminated reference must not swallow the rest of the prompt.
	if got := ResolveChain(st, "tail {{prompt:stack", 0); got != "tail {{prompt:stack" {
		t.Errorf("unterminated reference mangled: %q", got)
	}
}

// timeout is a short deadline used to prove termination.
func timeout() <-chan time.Time { return time.After(3 * time.Second) }
