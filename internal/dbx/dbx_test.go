package dbx

import "testing"

// Classify is a safety gate: a write mistaken for a read gets executed against
// a connection the user believed was read-only.
func TestClassify(t *testing.T) {
	reads := []string{
		"SELECT 1",
		"select * from users limit 10",
		"  \n SELECT id FROM t",
		"SHOW TABLES",
		"DESCRIBE users",
		"EXPLAIN SELECT * FROM t",
		"(SELECT 1)",
		"WITH x AS (SELECT 1) SELECT * FROM x",
		// A table whose name merely contains a write verb must not trip it.
		"SELECT * FROM updates",
		"SELECT * FROM deleted_rows",
		"SELECT insert_count FROM stats",
		// A comment must not decide the classification.
		"-- delete everything\nSELECT 1",
		"/* update */ SELECT 1",
	}
	for _, s := range reads {
		if got := Classify(s); got != KindRead {
			t.Errorf("Classify(%q) = %v, want read", s, got)
		}
	}

	writes := []string{
		"DELETE FROM users",
		"delete from users where id = 42",
		"INSERT INTO t VALUES (1)",
		"UPDATE t SET x = 1",
		"DROP TABLE users",
		"TRUNCATE t",
		"ALTER TABLE t ADD COLUMN c INT",
		"CREATE TABLE t (id INT)",
		"GRANT ALL ON t TO u",
		// A write hidden inside a CTE is the case a first-keyword check alone
		// would miss.
		"WITH d AS (DELETE FROM t RETURNING *) SELECT * FROM d",
		"WITH x AS (INSERT INTO t VALUES (1) RETURNING id) SELECT * FROM x",
		// A leading comment must not disguise a write.
		"/* select */ DELETE FROM users",
		"-- harmless\nDROP TABLE users",
	}
	for _, s := range writes {
		if got := Classify(s); got != KindWrite {
			t.Errorf("Classify(%q) = %v, want write", s, got)
		}
	}
}

// Stacked statements are the classic way to smuggle a second command past a
// check that only looked at the first.
func TestSingleStatement(t *testing.T) {
	single := []string{
		"SELECT 1",
		"SELECT 1;",
		"SELECT 1;   ",
		"SELECT 1; -- trailing comment",
		`SELECT ';' AS semi`,
		`SELECT "a;b"`,
		"SELECT ';;;'",
		"/* a; b */ SELECT 1",
	}
	for _, s := range single {
		if !SingleStatement(s) {
			t.Errorf("SingleStatement(%q) = false, want true", s)
		}
	}

	stacked := []string{
		"SELECT 1; DROP TABLE users",
		"SELECT 1;DELETE FROM t",
		"SELECT 1; SELECT 2",
		`SELECT ';'; DROP TABLE users`,
		"SELECT 1;\nUPDATE t SET x=1",
	}
	for _, s := range stacked {
		if SingleStatement(s) {
			t.Errorf("SingleStatement(%q) = true, want false", s)
		}
	}
}

func TestStripComments(t *testing.T) {
	cases := []struct{ in, want string }{
		{"SELECT 1 -- comment", "SELECT 1 "},
		{"SELECT 1 /* c */ + 2", "SELECT 1   + 2"},
		{"SELECT '-- not a comment'", "SELECT '-- not a comment'"},
		{"SELECT '/* nor this */'", "SELECT '/* nor this */'"},
	}
	for _, c := range cases {
		if got := stripComments(c.in); got != c.want {
			t.Errorf("stripComments(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRenderReadResult(t *testing.T) {
	r := &Result{
		Kind:     KindRead,
		Columns:  []string{"id", "email"},
		Rows:     [][]any{{1, "a@example.com"}, {2, nil}},
		RowCount: 2,
		Took:     "3ms",
	}
	out := r.Render()
	for _, want := range []string{"id", "email", "a@example.com", "NULL", "2 rows in 3ms"} {
		if !contains(out, want) {
			t.Errorf("rendered table missing %q:\n%s", want, out)
		}
	}

	r.Truncated = true
	if !contains(r.Render(), "capped at") {
		t.Error("a truncated result should say so")
	}

	empty := &Result{Kind: KindRead, Took: "1ms"}
	if !contains(empty.Render(), "No rows") {
		t.Error("an empty result should say so")
	}

	w := &Result{Kind: KindWrite, Affected: 7, Took: "5ms"}
	if !contains(w.Render(), "Rows affected: 7") {
		t.Errorf("write result rendered as %q", w.Render())
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
