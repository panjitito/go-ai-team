package claudefs

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// line builds one assistant record with known usage.
func line(i int, model string) string {
	return fmt.Sprintf(
		`{"type":"assistant","uuid":"u%d","sessionId":"s1","timestamp":"2026-09-04T10:%02d:00Z",`+
			`"message":{"role":"assistant","model":%q,"usage":{"input_tokens":10,"output_tokens":5,`+
			`"cache_creation_input_tokens":2,"cache_read_input_tokens":3},`+
			`"content":[{"type":"tool_use","id":"t%d","name":"Read","input":{}}]}}`+"\n", i, i%60, model, i)
}

func userLine(i int) string {
	return fmt.Sprintf(
		`{"type":"user","uuid":"x%d","sessionId":"s1","timestamp":"2026-09-04T10:%02d:30Z",`+
			`"message":{"role":"user","content":"hello"}}`+"\n", i, i%60)
}

// Resuming a parse must produce exactly what parsing the whole file produces.
// If it does not, the token meter drifts and nobody notices until the number is
// obviously absurd.
func TestIncrementalMatchesFullParse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.jsonl")

	var content string
	for i := 0; i < 40; i++ {
		content += line(i, "claude-opus-5")
		content += userLine(i)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	first, err := ParseTranscript(path)
	if err != nil {
		t.Fatal(err)
	}

	// Append more, including a second model, and parse again — this resumes.
	more := ""
	for i := 40; i < 70; i++ {
		more += line(i, "claude-haiku-4-5")
		more += userLine(i)
	}
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(more)
	f.Close()

	incremental, err := ParseTranscript(path)
	if err != nil {
		t.Fatal(err)
	}

	// The same file parsed cold, with no cached state at all.
	ForgetTranscript(path)
	full, err := ParseTranscript(path)
	if err != nil {
		t.Fatal(err)
	}

	if incremental.Total() != full.Total() {
		t.Errorf("total: incremental %d, full %d", incremental.Total(), full.Total())
	}
	for _, c := range []struct {
		name     string
		got, wnt int64
	}{
		{"input", incremental.InputTokens, full.InputTokens},
		{"output", incremental.OutputTokens, full.OutputTokens},
		{"cacheWrite", incremental.CacheWrite, full.CacheWrite},
		{"cacheRead", incremental.CacheRead, full.CacheRead},
	} {
		if c.got != c.wnt {
			t.Errorf("%s: incremental %d, full %d", c.name, c.got, c.wnt)
		}
	}
	if incremental.Messages != full.Messages || incremental.ToolUses != full.ToolUses {
		t.Errorf("counts differ: msgs %d/%d tools %d/%d",
			incremental.Messages, full.Messages, incremental.ToolUses, full.ToolUses)
	}
	if len(incremental.Models) != len(full.Models) {
		t.Errorf("models: incremental %v, full %v", incremental.Models, full.Models)
	}
	if !incremental.LastAt.Equal(full.LastAt) || !incremental.FirstAt.Equal(full.FirstAt) {
		t.Errorf("timestamps drifted: %v..%v vs %v..%v",
			incremental.FirstAt, incremental.LastAt, full.FirstAt, full.LastAt)
	}
	if first.Total() >= full.Total() {
		t.Error("the second parse should have counted more than the first")
	}
}

// A record still being written has no newline yet. Counting it would count half
// a record, and counting it again when the rest lands would double it.
func TestIncrementalIgnoresAPartialRecord(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.jsonl")

	if err := os.WriteFile(path, []byte(line(1, "claude-opus-5")), 0o600); err != nil {
		t.Fatal(err)
	}
	one, _ := ParseTranscript(path)

	// Half a record arrives.
	half := line(2, "claude-opus-5")
	half = half[:len(half)/2]
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(half)
	f.Close()

	partial, _ := ParseTranscript(path)
	if partial.Total() != one.Total() {
		t.Errorf("a half-written record was counted: %d vs %d", partial.Total(), one.Total())
	}

	// The rest lands.
	rest := line(2, "claude-opus-5")[len(half):]
	f, _ = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(rest)
	f.Close()

	whole, _ := ParseTranscript(path)
	ForgetTranscript(path)
	cold, _ := ParseTranscript(path)
	if whole.Total() != cold.Total() {
		t.Errorf("after the record completed: incremental %d, cold %d", whole.Total(), cold.Total())
	}
	if whole.Messages != 2 {
		t.Errorf("messages = %d, want 2", whole.Messages)
	}
}

// A file that shrank is not the file we counted. Resuming would produce a total
// built from two different conversations.
func TestIncrementalRestartsWhenTheFileShrinks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.jsonl")

	var big string
	for i := 0; i < 30; i++ {
		big += line(i, "claude-opus-5")
	}
	os.WriteFile(path, []byte(big), 0o600)
	before, _ := ParseTranscript(path)

	// Replaced by a shorter, different transcript at the same path.
	os.WriteFile(path, []byte(line(99, "claude-opus-5")), 0o600)
	after, _ := ParseTranscript(path)

	ForgetTranscript(path)
	cold, _ := ParseTranscript(path)

	if after.Total() != cold.Total() {
		t.Errorf("after shrinking: resumed %d, cold %d — stale state was carried over",
			after.Total(), cold.Total())
	}
	if after.Total() >= before.Total() {
		t.Errorf("a smaller file reported %d, was %d", after.Total(), before.Total())
	}
}

// Only the tail of a transcript is read for the conversation, but the messages
// must be the same ones a full read would have ended with.
func TestConversationTailMatchesFullRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.jsonl")

	var content string
	for i := 0; i < 500; i++ {
		content += userLine(i)
		content += line(i, "claude-opus-5")
	}
	os.WriteFile(path, []byte(content), 0o600)

	got, err := ParseConversation(path, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 20 {
		t.Fatalf("got %d messages, want 20", len(got))
	}
	// The last user turn in the file is index 499; it must be in the window.
	last := got[len(got)-1]
	if last.Role != "assistant" {
		t.Errorf("last message role = %q, want assistant", last.Role)
	}

	// A limit larger than the file has messages must still return them all,
	// which is the path that widens the window.
	all, err := ParseConversation(path, 5000)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1000 {
		t.Errorf("got %d messages for the whole file, want 1000", len(all))
	}
}

// The point of resuming is that the bytes already read are not read again.
// Timing is too flaky to assert, so this checks the thing timing was standing in
// for: after a parse, the offset covers the whole file, and after an append it
// resumes from where it stopped rather than from zero.
func TestIncrementalDoesNotRereadTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.jsonl")

	var content string
	for i := 0; i < 50; i++ {
		content += line(i, "claude-opus-5")
	}
	os.WriteFile(path, []byte(content), 0o600)

	ForgetTranscript(path)
	if _, err := ParseTranscript(path); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(path)
	r := loadResume(path, st.Size())
	if r == nil {
		t.Fatal("no resume state was kept; every poll would re-read the file")
	}
	if r.off != st.Size() {
		t.Fatalf("offset %d, file %d: the next parse would re-read %d bytes",
			r.off, st.Size(), st.Size()-r.off)
	}

	before := r.off
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(line(50, "claude-opus-5"))
	f.Close()

	if _, err := ParseTranscript(path); err != nil {
		t.Fatal(err)
	}
	st2, _ := os.Stat(path)
	r2 := loadResume(path, st2.Size())
	if r2 == nil || r2.off != st2.Size() {
		t.Fatalf("after appending, offset = %v want %d", r2, st2.Size())
	}
	if before == 0 {
		t.Fatal("the first parse recorded nothing")
	}
}
