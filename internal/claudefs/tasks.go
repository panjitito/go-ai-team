package claudefs

import (
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// The agent's plan, reconstructed from its own tool calls.
//
// Claude Code keeps a task list and shows it as it works: what it decided to do,
// what it has finished, and which item it is on right now. That list is the
// single most useful thing to see across a row of agents — "what is this one
// actually doing" — and none of it was reaching this app. The tool calls were
// rendered as unlabelled cards and the plan itself was invisible.
//
// It is assembled rather than read, because the CLI does not write the list
// anywhere: it writes the events that build it.
//
//	TaskCreate {subject, description, activeForm}
//	  → result "Task #3 created successfully: Extend the gateway"
//	TaskUpdate {taskId: "3", status: "in_progress"}
//
// The number is only in the *result*, so a task is identified by pairing the
// call with its answer. An update whose create scrolled out of the window still
// counts — the status is known even when the subject is not, and saying "task 3,
// done" is better than dropping it.

// Task is one item of the agent's plan.
type Task struct {
	ID      string `json:"id"`
	Subject string `json:"subject"`
	// Status is the CLI's own word: pending, in_progress, completed, cancelled.
	Status string `json:"status"`
	// ActiveForm is how the agent describes doing it — "Extending the gateway"
	// — which is what to show while it is the one in progress.
	ActiveForm string `json:"activeForm,omitempty"`
}

// Done reports whether this item needs no more work.
func (t Task) Done() bool { return t.Status == "completed" || t.Status == "cancelled" }

// taskNum pulls the number out of "Task #3 created successfully: …".
var taskNum = regexp.MustCompile(`(?i)task\s*#?(\d+)`)

// Tasks reconstructs the plan from a parsed conversation, in the order the
// items were created.
func Tasks(msgs []Message) []Task {
	byID := map[string]*Task{}
	// order remembers where each id first appeared, so a plan reads in the order
	// it was written rather than in the order it was last touched.
	order := map[string]int{}
	n := 0

	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Kind != BlockTool || b.Tool == nil {
				continue
			}
			switch b.Tool.Name {
			case "TaskCreate":
				id, ok := createdID(b.Tool.Result)
				if !ok {
					// Still running, or the result never arrived. Nothing can be
					// keyed on yet.
					continue
				}
				subject, active := taskFields(b.Tool.Input)
				t := ensure(byID, order, &n, id)
				t.Subject, t.ActiveForm = subject, active
				if t.Status == "" {
					t.Status = "pending"
				}
			case "TaskUpdate":
				id, status := updateFields(b.Tool.Input)
				if id == "" {
					continue
				}
				t := ensure(byID, order, &n, id)
				if status != "" {
					t.Status = status
				}
			}
		}
	}
	if len(byID) == 0 {
		return nil
	}

	out := make([]Task, 0, len(byID))
	for _, t := range byID {
		if t.Subject == "" {
			t.Subject = "Task " + t.ID
		}
		if t.Status == "" {
			t.Status = "pending"
		}
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool {
		// Numeric where the ids are numbers, which they are, so 10 does not sort
		// before 2.
		a, ea := strconv.Atoi(out[i].ID)
		b, eb := strconv.Atoi(out[j].ID)
		if ea == nil && eb == nil {
			return a < b
		}
		return order[out[i].ID] < order[out[j].ID]
	})
	return out
}

func ensure(byID map[string]*Task, order map[string]int, n *int, id string) *Task {
	if t, ok := byID[id]; ok {
		return t
	}
	t := &Task{ID: id}
	byID[id] = t
	order[id] = *n
	*n++
	return t
}

func createdID(result string) (string, bool) {
	m := taskNum.FindStringSubmatch(result)
	if m == nil {
		return "", false
	}
	return m[1], true
}

func taskFields(input string) (subject, active string) {
	var in struct {
		Subject    string `json:"subject"`
		ActiveForm string `json:"activeForm"`
	}
	if json.Unmarshal([]byte(input), &in) != nil {
		return "", ""
	}
	return strings.TrimSpace(in.Subject), strings.TrimSpace(in.ActiveForm)
}

func updateFields(input string) (id, status string) {
	var in struct {
		TaskID any    `json:"taskId"`
		Status string `json:"status"`
	}
	if json.Unmarshal([]byte(input), &in) != nil {
		return "", ""
	}
	switch v := in.TaskID.(type) {
	case string:
		id = strings.TrimPrefix(strings.TrimSpace(v), "#")
	case float64:
		id = strconv.Itoa(int(v))
	}
	return id, strings.TrimSpace(in.Status)
}

// TaskProgress counts how many of the plan's items are finished, and names the
// one being worked on.
func TaskProgress(tasks []Task) (done, total int, current string) {
	for _, t := range tasks {
		total++
		if t.Done() {
			done++
			continue
		}
		if t.Status == "in_progress" && current == "" {
			current = t.ActiveForm
			if current == "" {
				current = t.Subject
			}
		}
	}
	return done, total, current
}
