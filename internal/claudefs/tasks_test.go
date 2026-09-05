package claudefs

import (
	"os"
	"testing"
)

// Point this at a transcript from a real session to check the plan parser
// against real data:
//
//	GAT_TRANSCRIPT=~/.claude/projects/…/<id>.jsonl go test ./internal/claudefs/ -run Real -v
//
// The fixtures above carry the shapes; this carries the volume, and no real
// transcript belongs in the repository — they are somebody's actual work.
func TestTasksAgainstARealTranscript(t *testing.T) {
	path := os.Getenv("GAT_TRANSCRIPT")
	if path == "" {
		t.Skip("set GAT_TRANSCRIPT to a transcript to check this")
	}
	msgs, err := ParseConversation(path, 1000)
	if err != nil {
		t.Fatal(err)
	}
	tasks := Tasks(msgs)
	done, total, current := TaskProgress(tasks)
	t.Logf("%d messages, %d tasks, %d done, on: %q", len(msgs), total, done, current)
	for _, x := range tasks {
		t.Logf("  #%s %-11s %s", x.ID, x.Status, x.Subject)
	}
	for _, x := range tasks {
		if x.Status == "" {
			t.Errorf("task %s has no status", x.ID)
		}
		if x.Subject == "" {
			t.Errorf("task %s has no subject, not even a placeholder", x.ID)
		}
	}
}

// The shapes here are copied from real transcripts: TaskCreate carries the
// subject but not the number, the number comes back in the result text, and
// TaskUpdate refers to it by that number.
func tool(name, input, result string) Block {
	return Block{Kind: BlockTool, Tool: &ToolCall{Name: name, Input: input, Result: result}}
}

func TestTasks(t *testing.T) {
	msgs := []Message{{Role: "assistant", Blocks: []Block{
		tool("TaskCreate", `{"subject":"Add UOM rate columns migration","activeForm":"Adding UOM rate columns migration"}`,
			"Task #1 created successfully: Add UOM rate columns migration"),
		tool("TaskCreate", `{"subject":"Extend DirectVpGateway","activeForm":"Extending DirectVpGateway"}`,
			"Task #2 created successfully: Extend DirectVpGateway"),
		tool("TaskCreate", `{"subject":"Update the emails","activeForm":"Updating the emails"}`,
			"Task #3 created successfully: Update the emails"),
		tool("TaskUpdate", `{"taskId":"1","status":"in_progress"}`, "ok"),
		tool("TaskUpdate", `{"taskId":"1","status":"completed"}`, "ok"),
		tool("TaskUpdate", `{"taskId":"2","status":"in_progress"}`, "ok"),
	}}}

	got := Tasks(msgs)
	if len(got) != 3 {
		t.Fatalf("got %d tasks, want 3: %+v", len(got), got)
	}
	if got[0].Status != "completed" || got[1].Status != "in_progress" || got[2].Status != "pending" {
		t.Errorf("statuses = %q/%q/%q", got[0].Status, got[1].Status, got[2].Status)
	}
	if got[1].Subject != "Extend DirectVpGateway" {
		t.Errorf("subject = %q", got[1].Subject)
	}

	done, total, current := TaskProgress(got)
	if done != 1 || total != 3 {
		t.Errorf("progress = %d/%d, want 1/3", done, total)
	}
	// The active form is what to show while it is the one being worked on: it
	// reads as something happening rather than as a heading.
	if current != "Extending DirectVpGateway" {
		t.Errorf("current = %q", current)
	}
}

// The transcript is read tail-first, so a plan started earlier can arrive with
// its creates off the end of the window. Knowing a task is done is still worth
// more than dropping it.
func TestTasksWithoutTheirCreate(t *testing.T) {
	msgs := []Message{{Role: "assistant", Blocks: []Block{
		tool("TaskUpdate", `{"taskId":"7","status":"completed"}`, "ok"),
		tool("TaskUpdate", `{"taskId":"8","status":"in_progress"}`, "ok"),
	}}}
	got := Tasks(msgs)
	if len(got) != 2 {
		t.Fatalf("got %d tasks, want 2: %+v", len(got), got)
	}
	if got[0].Subject != "Task 7" || got[0].Status != "completed" {
		t.Errorf("task 7 = %+v", got[0])
	}
}

// A create still running has no number yet, so there is nothing to key it on.
func TestTasksIgnoresAPendingCreate(t *testing.T) {
	msgs := []Message{{Role: "assistant", Blocks: []Block{
		{Kind: BlockTool, Tool: &ToolCall{Name: "TaskCreate", Input: `{"subject":"x"}`, Pending: true}},
	}}}
	if got := Tasks(msgs); len(got) != 0 {
		t.Errorf("got %+v, want nothing until the number comes back", got)
	}
}

func TestTasksNoneAtAll(t *testing.T) {
	msgs := []Message{{Role: "assistant", Blocks: []Block{
		tool("Bash", `{"command":"ls"}`, "a b c"),
		{Kind: BlockText, Text: "hello"},
	}}}
	if got := Tasks(msgs); got != nil {
		t.Errorf("got %+v, want nothing", got)
	}
}

// Ten must not sort in front of two.
func TestTasksSortNumerically(t *testing.T) {
	var blocks []Block
	for _, n := range []string{"2", "10", "1"} {
		blocks = append(blocks, tool("TaskCreate", `{"subject":"s`+n+`"}`,
			"Task #"+n+" created successfully: s"+n))
	}
	got := Tasks([]Message{{Role: "assistant", Blocks: blocks}})
	want := []string{"1", "2", "10"}
	for i, w := range want {
		if got[i].ID != w {
			t.Errorf("order = %v, want %v", []Task{got[0], got[1], got[2]}, want)
			break
		}
	}
}
