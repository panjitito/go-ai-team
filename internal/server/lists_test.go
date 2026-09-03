package server

import (
	"encoding/json"
	"testing"
)

// A list endpoint that sometimes answers `null` instead of `[]` is a broken
// contract, and it really did crash a whole view: the Terminals tab threw
// "Cannot read properties of null" on a project with no saved commands. This
// pins the fix.
func TestOrEmptyEncodesAsArray(t *testing.T) {
	var nilSlice []string
	b, err := json.Marshal(orEmpty(nilSlice))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "[]" {
		t.Errorf("a nil slice encoded as %s, want []", b)
	}

	// Without the helper, this is what the client used to receive.
	raw, _ := json.Marshal(nilSlice)
	if string(raw) != "null" {
		t.Fatalf("precondition changed: a nil slice now encodes as %s", raw)
	}

	// A populated slice must pass through untouched.
	b, _ = json.Marshal(orEmpty([]string{"a", "b"}))
	if string(b) != `["a","b"]` {
		t.Errorf("a populated slice encoded as %s", b)
	}

	// It must work for structs too, which is what the real endpoints return.
	type row struct {
		Name string `json:"name"`
	}
	var nilRows []row
	b, _ = json.Marshal(orEmpty(nilRows))
	if string(b) != "[]" {
		t.Errorf("a nil struct slice encoded as %s, want []", b)
	}
}
