package ai

import "testing"

// A model asked for JSON will still wrap it in prose or a code fence sometimes,
// so the reply is salvaged rather than trusted. This is the single most common
// failure of the pattern, which is why it is pinned here.
func TestExtractJSON(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{`{"a":1}`, `{"a":1}`, true},
		{"here you go:\n```json\n{\"a\":1}\n```", `{"a":1}`, true},
		{`prose [1,2,3] more`, `[1,2,3]`, true},
		{`{"s":"a}b"}`, `{"s":"a}b"}`, true},
		{`{"nested":{"x":[1,{"y":2}]}}`, `{"nested":{"x":[1,{"y":2}]}}`, true},
		{`no json here`, "", false},
		{`{"unterminated": `, "", false},
	}
	for _, c := range cases {
		got, ok := extractJSON(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("extractJSON(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}
