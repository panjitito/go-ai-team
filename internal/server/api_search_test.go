package server

import "testing"

// A conversation's project, named.
//
// The directory the transcripts sit in is the working path with every
// separator flattened to a dash, and that is not reversible: a folder called
// "mis-dashboard" and one called "mis/dashboard" encode identically. So the
// name comes from the cwd the record itself carries.
func TestSearchProjectLabel(t *testing.T) {
	known := map[string]string{
		`c:\users\tito\documents\projects\mis-dashboard`: "MIS Dashboard",
	}
	cases := []struct{ cwd, want string }{
		// A project the app knows is called what the person called it.
		{`C:\Users\TITO\Documents\Projects\mis-dashboard`, "MIS Dashboard"},
		// Same directory, spelled the other way, because this is Windows.
		{`c:\users\tito\documents\projects\MIS-Dashboard`, "MIS Dashboard"},
		// Trailing separator is the same directory too.
		{`C:\Users\TITO\Documents\Projects\mis-dashboard\`, "MIS Dashboard"},
		// One nobody added still gets the folder's own name.
		{`C:\Users\TITO\Documents\Projects\Android`, "Android"},
		// A dash in the folder name survives, which the old reversal could not
		// have managed.
		{`D:\work\some-thing`, "some-thing"},
		// Nothing recorded, nothing claimed.
		{"", ""},
	}
	for _, c := range cases {
		if got := projectLabel(known, c.cwd); got != c.want {
			t.Errorf("projectLabel(%q) = %q, want %q", c.cwd, got, c.want)
		}
	}
}
