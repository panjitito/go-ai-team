package store

import (
	"path/filepath"
	"testing"
	"time"
)

// newTestStore builds an in-memory-ish store rooted in t.TempDir so a test never
// touches the user's real ~/.goaiteam.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	root := t.TempDir()
	return &Store{
		root: root,
		path: filepath.Join(root, "state.json"),
		state: State{
			Version:  1,
			Settings: Settings{AutoSwitch: true, Port: 7777},
		},
	}
}

// The cascade is the feature's spine: agent override, project pin, nearest
// folder that pins an account, global default, then the system directory. Each
// step is asserted here, including the fall-through when a reference dangles.
func TestResolveAccount_Cascade(t *testing.T) {
	s := newTestStore(t)

	work := &Account{ID: "acc_work", Name: "Work", Provider: ProviderClaude, Dir: `C:\p\work`, Color: "#f00"}
	personal := &Account{ID: "acc_personal", Name: "Personal", Provider: ProviderClaude, Dir: `C:\p\personal`, Color: "#0f0"}
	client := &Account{ID: "acc_client", Name: "Client", Provider: ProviderClaude, Dir: `C:\p\client`, Color: "#00f"}
	for _, a := range []*Account{work, personal, client} {
		if err := s.AddAccount(a); err != nil {
			t.Fatal(err)
		}
	}
	// AddAccount makes the first account the global default.
	if got := s.Settings().DefaultAccountID; got != "acc_work" {
		t.Fatalf("default account = %q, want acc_work", got)
	}

	// outer(personal) > inner(no pin) > project > agent
	outer := &Folder{ID: "fld_outer", Name: "Company", AccountID: "acc_personal"}
	inner := &Folder{ID: "fld_inner", Name: "Repos", ParentID: "fld_outer"}
	if err := s.AddFolder(outer); err != nil {
		t.Fatal(err)
	}
	if err := s.AddFolder(inner); err != nil {
		t.Fatal(err)
	}

	proj := &Project{ID: "prj_1", Name: "app", Path: `C:\src\app`, FolderID: "fld_inner"}
	if err := s.AddProject(proj); err != nil {
		t.Fatal(err)
	}
	agent := &Agent{ID: "agt_1", ProjectID: "prj_1", Name: "Backend", Provider: ProviderClaude}
	if err := s.AddAgent(agent); err != nil {
		t.Fatal(err)
	}

	check := func(label, wantID, wantSource string) {
		t.Helper()
		r := s.ResolveAccount("agt_1", "prj_1", ProviderClaude)
		if r.AccountID != wantID || r.Source != wantSource {
			t.Errorf("%s: got (%s, %s), want (%s, %s)", label, r.AccountID, r.Source, wantID, wantSource)
		}
	}

	// Nothing pinned anywhere except the folder chain: the nearest ancestor
	// that pins an account wins, which is fld_outer via fld_inner.
	check("folder inherited through parent", "acc_personal", "folder")

	// A nearer folder pin beats the outer one.
	if _, err := s.UpdateFolder("fld_inner", func(f *Folder) { f.AccountID = "acc_client" }); err != nil {
		t.Fatal(err)
	}
	check("nearest folder wins", "acc_client", "folder")

	// A project pin beats any folder.
	if _, err := s.UpdateProject("prj_1", func(p *Project) { p.AccountID = "acc_work" }); err != nil {
		t.Fatal(err)
	}
	check("project beats folder", "acc_work", "project")

	// An agent override beats everything.
	if _, err := s.UpdateAgent("agt_1", func(a *Agent) { a.AccountID = "acc_personal" }); err != nil {
		t.Fatal(err)
	}
	check("agent beats project", "acc_personal", "agent")

	// Unpinning walks back down the chain in order.
	if _, err := s.UpdateAgent("agt_1", func(a *Agent) { a.AccountID = "" }); err != nil {
		t.Fatal(err)
	}
	check("back to project", "acc_work", "project")
	if _, err := s.UpdateProject("prj_1", func(p *Project) { p.AccountID = "" }); err != nil {
		t.Fatal(err)
	}
	check("back to nearest folder", "acc_client", "folder")
	if _, err := s.UpdateFolder("fld_inner", func(f *Folder) { f.AccountID = "" }); err != nil {
		t.Fatal(err)
	}
	check("back to outer folder", "acc_personal", "folder")
	if _, err := s.UpdateFolder("fld_outer", func(f *Folder) { f.AccountID = "" }); err != nil {
		t.Fatal(err)
	}
	check("back to global default", "acc_work", "default")
}

// Deleting an account must not break the agents that referenced it: every
// reference degrades to the next cascade step instead of erroring.
func TestResolveAccount_DanglingReferenceFallsThrough(t *testing.T) {
	s := newTestStore(t)
	keep := &Account{ID: "acc_keep", Name: "Keep", Provider: ProviderClaude, Dir: `C:\p\keep`}
	gone := &Account{ID: "acc_gone", Name: "Gone", Provider: ProviderClaude, Dir: `C:\p\gone`}
	_ = s.AddAccount(keep)
	_ = s.AddAccount(gone)
	_ = s.AddProject(&Project{ID: "prj_1", Path: `C:\src`, AccountID: "acc_gone"})
	_ = s.AddAgent(&Agent{ID: "agt_1", ProjectID: "prj_1", AccountID: "acc_gone", Provider: ProviderClaude})

	if r := s.ResolveAccount("agt_1", "prj_1", ProviderClaude); r.AccountID != "acc_gone" {
		t.Fatalf("precondition: got %q", r.AccountID)
	}
	if err := s.DeleteAccount("acc_gone"); err != nil {
		t.Fatal(err)
	}
	r := s.ResolveAccount("agt_1", "prj_1", ProviderClaude)
	if r.AccountID != "acc_keep" || r.Source != "default" {
		t.Errorf("after delete got (%s, %s), want (acc_keep, default)", r.AccountID, r.Source)
	}
}

// With no accounts at all the cascade must land on the provider's own
// directory rather than returning an empty, unusable answer.
func TestResolveAccount_SystemDefault(t *testing.T) {
	s := newTestStore(t)
	_ = s.AddProject(&Project{ID: "prj_1", Path: `C:\src`})
	r := s.ResolveAccount("", "prj_1", ProviderClaude)
	if r.Source != "system" || r.Dir != "" {
		t.Errorf("got (%s, dir=%q), want (system, dir=\"\")", r.Source, r.Dir)
	}
}

// An account of a different provider must never satisfy a resolution, or a
// Codex pin would silently launch a Claude agent.
func TestResolveAccount_ProviderIsolation(t *testing.T) {
	s := newTestStore(t)
	_ = s.AddAccount(&Account{ID: "acc_codex", Name: "Codex", Provider: ProviderCodex, Dir: `C:\p\codex`})
	_ = s.AddProject(&Project{ID: "prj_1", Path: `C:\src`, AccountID: "acc_codex"})
	_ = s.AddAgent(&Agent{ID: "agt_1", ProjectID: "prj_1", AccountID: "acc_codex", Provider: ProviderClaude})

	r := s.ResolveAccount("agt_1", "prj_1", ProviderClaude)
	if r.AccountID != "" || r.Source != "system" {
		t.Errorf("a Codex account satisfied a Claude resolution: got (%s, %s)", r.AccountID, r.Source)
	}
	if r := s.ResolveAccount("agt_1", "prj_1", ProviderCodex); r.AccountID != "acc_codex" {
		t.Errorf("Codex resolution failed: got %q", r.AccountID)
	}
}

// A folder cycle must not hang the resolver.
func TestResolveAccount_FolderCycleTerminates(t *testing.T) {
	s := newTestStore(t)
	_ = s.AddFolder(&Folder{ID: "a", ParentID: "b"})
	_ = s.AddFolder(&Folder{ID: "b", ParentID: "a"})
	_ = s.AddProject(&Project{ID: "prj_1", Path: `C:\src`, FolderID: "a"})
	done := make(chan Resolution, 1)
	go func() { done <- s.ResolveAccount("", "prj_1", ProviderClaude) }()
	select {
	case r := <-done:
		if r.Source != "system" {
			t.Errorf("got source %q, want system", r.Source)
		}
	case <-timeAfter():
		t.Fatal("ResolveAccount did not terminate on a folder cycle")
	}
}

func TestProviderEnvVars(t *testing.T) {
	cases := map[Provider]string{
		ProviderClaude: "CLAUDE_CONFIG_DIR",
		ProviderCodex:  "CODEX_HOME",
		ProviderGrok:   "GROK_HOME",
		ProviderCursor: "CURSOR_CONFIG_DIR",
	}
	for p, want := range cases {
		if got := p.EnvVar(); got != want {
			t.Errorf("%s.EnvVar() = %q, want %q", p, got, want)
		}
	}
}

func TestDeleteProjectRemovesItsAgents(t *testing.T) {
	s := newTestStore(t)
	_ = s.AddProject(&Project{ID: "prj_1", Path: `C:\a`})
	_ = s.AddProject(&Project{ID: "prj_2", Path: `C:\b`})
	_ = s.AddAgent(&Agent{ID: "agt_1", ProjectID: "prj_1"})
	_ = s.AddAgent(&Agent{ID: "agt_2", ProjectID: "prj_2"})
	if err := s.DeleteProject("prj_1"); err != nil {
		t.Fatal(err)
	}
	agents := s.Agents()
	if len(agents) != 1 || agents[0].ID != "agt_2" {
		t.Errorf("agents after delete = %+v, want only agt_2", agents)
	}
}

// timeAfter is a short deadline used to prove termination.
func timeAfter() <-chan time.Time { return time.After(2 * time.Second) }
