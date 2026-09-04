package claudefs

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// Reading the conversation out of the transcript.
//
// The terminal shows what the CLI drew; the transcript holds what actually
// happened, already structured. Rendering from the transcript is what lets the
// UI be a conversation rather than a screen scrape: prose is prose, a tool call
// is a tool call with its own input and result, and none of it depends on
// parsing ANSI or guessing where a box drawing ended.
//
// Two things are deliberately dropped. `thinking` blocks are the model's private
// reasoning and are not shown. And a user record whose content is nothing but
// tool_result blocks is not a user turn at all — it is the machinery answering
// the previous tool call, and rendering it as something the person said would be
// actively misleading.

// BlockKind distinguishes prose from a tool call inside one message.
type BlockKind string

const (
	BlockText BlockKind = "text"
	BlockTool BlockKind = "tool"
)

// Block is one ordered piece of a message.
type Block struct {
	Kind BlockKind `json:"kind"`
	Text string    `json:"text,omitempty"`
	Tool *ToolCall `json:"tool,omitempty"`
}

// ToolCall is one tool invocation and its result.
type ToolCall struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Summary is the one-line description shown on the collapsed card: the file
	// path, the command, the pattern. It is what makes a wall of tool calls
	// skimmable.
	Summary string `json:"summary,omitempty"`
	Input   string `json:"input,omitempty"`
	Result  string `json:"result,omitempty"`
	IsError bool   `json:"isError,omitempty"`
	// Pending is true while the result has not arrived, which is how the UI
	// shows a tool still running.
	Pending bool `json:"pending"`
}

// Message is one turn in the conversation.
type Message struct {
	ID     string    `json:"id"`
	Role   string    `json:"role"`
	Blocks []Block   `json:"blocks"`
	When   time.Time `json:"when"`
	Model  string    `json:"model,omitempty"`
}

// convLine is the transcript shape this parser needs.
type convLine struct {
	Type      string `json:"type"`
	UUID      string `json:"uuid"`
	Timestamp string `json:"timestamp"`
	Message   struct {
		Role    string          `json:"role"`
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// convBlock is one content block.
type convBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`

	// tool_use
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`

	// tool_result
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// ParseConversation reads a transcript into renderable messages.
//
// Consecutive assistant output is merged into one message, because Claude emits
// a separate record per API response and showing each as its own bubble turns a
// single answer into a dozen fragments.
func ParseConversation(path string, limit int) ([]Message, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 256*1024), 32*1024*1024)

	var msgs []Message
	// byToolID lets a tool_result find the tool_use it answers, which may be
	// several records earlier.
	byToolID := map[string]*ToolCall{}

	appendAssistant := func(when time.Time, model string, uuid string) *Message {
		if n := len(msgs); n > 0 && msgs[n-1].Role == "assistant" {
			if !when.IsZero() {
				msgs[n-1].When = when
			}
			if model != "" {
				msgs[n-1].Model = model
			}
			return &msgs[n-1]
		}
		msgs = append(msgs, Message{ID: uuid, Role: "assistant", When: when, Model: model})
		return &msgs[len(msgs)-1]
	}

	for sc.Scan() {
		raw := sc.Bytes()
		if len(raw) == 0 || raw[0] != '{' {
			continue
		}
		var l convLine
		if json.Unmarshal(raw, &l) != nil {
			continue
		}
		if l.Type != "user" && l.Type != "assistant" {
			continue
		}
		when, _ := time.Parse(time.RFC3339Nano, l.Timestamp)

		// A plain string is always a real person speaking.
		if len(l.Message.Content) > 0 && l.Message.Content[0] == '"' {
			var s string
			if json.Unmarshal(l.Message.Content, &s) == nil && strings.TrimSpace(s) != "" {
				msgs = append(msgs, Message{
					ID: l.UUID, Role: "user", When: when,
					Blocks: []Block{{Kind: BlockText, Text: s}},
				})
			}
			continue
		}

		var blocks []convBlock
		if json.Unmarshal(l.Message.Content, &blocks) != nil {
			continue
		}

		if l.Type == "assistant" {
			for _, b := range blocks {
				switch b.Type {
				case "text":
					if strings.TrimSpace(b.Text) == "" {
						continue
					}
					m := appendAssistant(when, l.Message.Model, l.UUID)
					m.Blocks = append(m.Blocks, Block{Kind: BlockText, Text: b.Text})
				case "tool_use":
					tc := &ToolCall{
						ID:      b.ID,
						Name:    b.Name,
						Summary: summariseTool(b.Name, b.Input),
						Input:   prettyJSON(b.Input),
						Pending: true,
					}
					byToolID[b.ID] = tc
					m := appendAssistant(when, l.Message.Model, l.UUID)
					m.Blocks = append(m.Blocks, Block{Kind: BlockTool, Tool: tc})
				}
				// thinking is deliberately skipped.
			}
			continue
		}

		// A user record: either the person, or tool results answering the CLI.
		var userText []string
		for _, b := range blocks {
			switch b.Type {
			case "text":
				if strings.TrimSpace(b.Text) != "" {
					userText = append(userText, b.Text)
				}
			case "tool_result":
				tc, ok := byToolID[b.ToolUseID]
				if !ok {
					continue
				}
				tc.Pending = false
				tc.IsError = b.IsError
				tc.Result = flattenResult(b.Content)
			}
		}
		if len(userText) > 0 {
			msgs = append(msgs, Message{
				ID: l.UUID, Role: "user", When: when,
				Blocks: []Block{{Kind: BlockText, Text: strings.Join(userText, "\n\n")}},
			})
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	// Keep the tail: a long session's early turns are not what someone opening
	// the window wants to see first, and sending all of them is wasteful.
	if limit > 0 && len(msgs) > limit {
		msgs = msgs[len(msgs)-limit:]
	}
	return msgs, nil
}

// summariseTool produces the one-line label on a collapsed tool card.
//
// Each tool's most useful field differs, and a generic dump of the input is
// unreadable, so the common ones are named explicitly.
func summariseTool(name string, input json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(input, &m) != nil {
		return ""
	}
	str := func(k string) string {
		if v, ok := m[k].(string); ok {
			return v
		}
		return ""
	}
	switch name {
	case "Bash", "BashOutput":
		if d := str("description"); d != "" {
			return d
		}
		return firstLine(str("command"), 120)
	case "Read", "Write", "Edit", "MultiEdit", "NotebookEdit":
		return shortPath(str("file_path") + str("notebook_path"))
	case "Glob":
		return str("pattern")
	case "Grep":
		p := str("pattern")
		if in := str("path"); in != "" {
			return p + "  in " + shortPath(in)
		}
		return p
	case "WebFetch", "WebSearch":
		if u := str("url"); u != "" {
			return u
		}
		return str("query")
	case "Task", "Agent":
		return str("description")
	case "TodoWrite":
		return "updating the todo list"
	}
	// Unknown tool: show the first string field, which is nearly always the
	// interesting one.
	for _, k := range []string{"description", "path", "file_path", "query", "prompt", "command"} {
		if v := str(k); v != "" {
			return firstLine(v, 120)
		}
	}
	return ""
}

// shortPath trims a long absolute path to something readable, keeping the tail
// because the file name matters more than the drive letter.
func shortPath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	if len(p) <= 60 {
		return p
	}
	parts := strings.Split(p, "/")
	if len(parts) <= 3 {
		return p
	}
	return ".../" + strings.Join(parts[len(parts)-3:], "/")
}

func firstLine(s string, max int) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// flattenResult turns a tool result into text. Results arrive either as a plain
// string or as a block array, and very large ones are trimmed: an agent that
// read a 4000-line file should not push that whole file into the browser.
func flattenResult(raw json.RawMessage) string {
	const cap = 8000
	trim := func(s string) string {
		if len(s) <= cap {
			return s
		}
		return s[:cap] + fmt.Sprintf("\n\n… %d more characters not shown …", len(s)-cap)
	}
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return trim(s)
		}
		return ""
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		var parts []string
		for _, b := range blocks {
			if b.Text != "" {
				parts = append(parts, b.Text)
			} else if b.Type != "" && b.Type != "text" {
				// An image or other non-text result: say so rather than
				// rendering nothing at all.
				parts = append(parts, "["+b.Type+"]")
			}
		}
		return trim(strings.Join(parts, "\n"))
	}
	return trim(string(raw))
}

// prettyJSON renders tool input for the expanded card.
func prettyJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return string(raw)
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return string(raw)
	}
	s := string(b)
	if len(s) > 4000 {
		s = s[:4000] + "\n…"
	}
	return s
}
