package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/uniair/go-ai-team/internal/store"
)

// Each function here is one metered feature in the tool we are competing with,
// implemented against the user's own subscription instead. The prompts are kept
// short and the models cheap, because the whole point is that running these
// should never be something the user has to budget for.

// CommitMessage writes a Conventional Commits message from a real diff.
func (r *Runner) CommitMessage(ctx context.Context, dir, diff, draft, format string) (string, error) {
	if strings.TrimSpace(diff) == "" {
		return "", fmt.Errorf("nothing staged to describe")
	}
	// A huge diff would cost more than the answer is worth and adds nothing: the
	// shape of the change is legible from the first few hundred lines.
	if len(diff) > 24000 {
		diff = diff[:24000] + "\n… diff truncated …"
	}
	style := format
	if style == "" {
		style = "Conventional Commits: type(scope): subject, imperative mood, lower case, no trailing period, subject under 72 characters."
	}
	var b strings.Builder
	b.WriteString("Write one commit message for this diff.\n\nStyle: ")
	b.WriteString(style)
	b.WriteString("\n")
	if strings.TrimSpace(draft) != "" {
		b.WriteString("\nThe author's rough draft, which describes their intent — keep that intent and fix the wording:\n")
		b.WriteString(draft)
		b.WriteString("\n")
	}
	b.WriteString("\nOutput the commit message and nothing else. No explanation, no quotes, no code fence.\n\n--- diff ---\n")
	b.WriteString(diff)

	out, err := r.Run(ctx, b.String(), Opts{Dir: dir, Timeout: 60 * time.Second})
	if err != nil {
		return "", err
	}
	return cleanOneLiner(out), nil
}

// cleanOneLiner strips the wrappers a model adds around a short answer.
func cleanOneLiner(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.Index(s, "\n"); i >= 0 {
			s = s[i+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	s = strings.TrimSpace(s)
	// A model sometimes answers with the whole thing in quotes.
	if len(s) > 1 && ((s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'')) {
		inner := s[1 : len(s)-1]
		if !strings.Contains(inner, "\"") {
			s = inner
		}
	}
	return strings.TrimSpace(s)
}

// Suggestion is one recommended agent for a task.
type Suggestion struct {
	Role   string `json:"role"`
	Reason string `json:"reason"`
	Model  string `json:"model,omitempty"`
}

// SuggestAgent picks the best-suited role for a described task.
func (r *Runner) SuggestAgent(ctx context.Context, task string, roles []store.RoleDef) ([]Suggestion, error) {
	if strings.TrimSpace(task) == "" {
		return nil, fmt.Errorf("describe the task first")
	}
	var cat strings.Builder
	for _, x := range roles {
		cat.WriteString("- ")
		cat.WriteString(x.Name)
		if x.Tagline != "" {
			cat.WriteString(": ")
			cat.WriteString(x.Tagline)
		}
		if x.BestFor != "" {
			cat.WriteString(" (best for ")
			cat.WriteString(x.BestFor)
			cat.WriteString(")")
		}
		cat.WriteString("\n")
	}
	prompt := fmt.Sprintf(
		"A developer wants to: %s\n\nPick the 3 best-suited roles from this catalogue, best first.\n\n%s\n"+
			"Reply as JSON: {\"suggestions\":[{\"role\":\"exact name from the list\","+
			"\"reason\":\"one short sentence\",\"model\":\"haiku|sonnet|opus\"}]}",
		task, cat.String())

	var res struct {
		Suggestions []Suggestion `json:"suggestions"`
	}
	if err := r.RunJSON(ctx, prompt, &res, Opts{Timeout: 60 * time.Second}); err != nil {
		return nil, err
	}
	// Only return roles that actually exist, so a hallucinated name cannot
	// reach the UI as a broken choice.
	valid := map[string]bool{}
	for _, x := range roles {
		valid[strings.ToLower(x.Name)] = true
	}
	var out []Suggestion
	for _, s := range res.Suggestions {
		if valid[strings.ToLower(s.Role)] {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no usable suggestion came back")
	}
	return out, nil
}

// ModelPick is an adaptive-mode recommendation.
type ModelPick struct {
	Model  string `json:"model"`
	Reason string `json:"reason"`
}

// PickModel right-sizes the model for a prompt before it is sent, so a
// translation or a one-line fix does not run on a flagship.
func (r *Runner) PickModel(ctx context.Context, prompt string) (ModelPick, error) {
	var out ModelPick
	if strings.TrimSpace(prompt) == "" {
		return out, fmt.Errorf("nothing to analyse")
	}
	if len(prompt) > 4000 {
		prompt = prompt[:4000]
	}
	q := "Classify the difficulty of this coding request and choose the cheapest model that will do it well.\n\n" +
		"haiku: mechanical edits, renames, translations, formatting, single-file tweaks, questions with a short factual answer.\n" +
		"sonnet: ordinary feature work, multi-file changes, tests, debugging with a clear reproduction.\n" +
		"opus: architecture, wide refactors, subtle concurrency or correctness problems, anything needing a plan first.\n\n" +
		"Request:\n" + prompt + "\n\n" +
		"Reply as JSON: {\"model\":\"haiku|sonnet|opus\",\"reason\":\"one short sentence\"}"

	if err := r.RunJSON(ctx, q, &out, Opts{Timeout: 45 * time.Second}); err != nil {
		return out, err
	}
	switch strings.ToLower(out.Model) {
	case "haiku", "sonnet", "opus":
		out.Model = strings.ToLower(out.Model)
	default:
		return out, fmt.Errorf("unexpected model %q", out.Model)
	}
	return out, nil
}

// Cluster is one theme found in a pile of raw feedback.
type Cluster struct {
	Theme string   `json:"theme"`
	IDs   []string `json:"ids"`
	// Impact and Effort are 1-5, for the matrix view.
	Impact int    `json:"impact"`
	Effort int    `json:"effort"`
	Ticket string `json:"ticket,omitempty"`
}

// ClusterIdeas groups raw feedback into themes, deduplicating as it goes. This
// is the Idea Radar: the same corpus becomes themes, an impact/effort matrix and
// a proposed next ticket.
func (r *Runner) ClusterIdeas(ctx context.Context, ideas []*store.Idea) ([]Cluster, error) {
	if len(ideas) == 0 {
		return nil, fmt.Errorf("no ideas to sort")
	}
	var b strings.Builder
	b.WriteString("Group this user feedback into themes. Merge duplicates. ")
	b.WriteString("Rate each theme's impact and effort from 1 to 5, and propose a single actionable ticket title for it.\n\n")
	for _, i := range ideas {
		body := i.Body
		if len(body) > 400 {
			body = body[:400]
		}
		fmt.Fprintf(&b, "- id=%s: %s\n", i.ID, strings.ReplaceAll(body, "\n", " "))
	}
	b.WriteString("\nReply as JSON: {\"clusters\":[{\"theme\":\"short name\",\"ids\":[\"id\",...]," +
		"\"impact\":1-5,\"effort\":1-5,\"ticket\":\"proposed ticket title\"}]}")

	var res struct {
		Clusters []Cluster `json:"clusters"`
	}
	if err := r.RunJSON(ctx, b.String(), &res, Opts{Timeout: 120 * time.Second, Model: "sonnet"}); err != nil {
		return nil, err
	}
	// Drop ids the model invented, so promoting a cluster cannot touch a
	// nonexistent idea.
	known := map[string]bool{}
	for _, i := range ideas {
		known[i.ID] = true
	}
	var out []Cluster
	for _, c := range res.Clusters {
		var ids []string
		for _, id := range c.IDs {
			if known[id] {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			continue
		}
		c.IDs = ids
		c.Impact = clamp(c.Impact, 1, 5)
		c.Effort = clamp(c.Effort, 1, 5)
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("nothing could be clustered")
	}
	return out, nil
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Summarise condenses text to roughly the requested spoken length, which is what
// makes a long changelog answerable in ten seconds.
func (r *Runner) Summarise(ctx context.Context, text string, seconds int) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("nothing to summarise")
	}
	if len(text) > 40000 {
		text = text[:40000] + "\n… truncated …"
	}
	if seconds <= 0 {
		seconds = 30
	}
	// Rough speaking rate, 150 words a minute.
	words := seconds * 150 / 60
	q := fmt.Sprintf(
		"Summarise the following in about %d words, as flowing prose meant to be read aloud. "+
			"No headings, no bullet points, no markdown, no code. Lead with what changed and why it matters.\n\n%s",
		words, text)
	return r.Run(ctx, q, Opts{Timeout: 120 * time.Second, Model: "sonnet"})
}

// ScopeTicket turns fuzzy feedback into a concrete brief, read against the real
// codebase, before any code is written.
func (r *Runner) ScopeTicket(ctx context.Context, dir, title, body string) (string, error) {
	q := fmt.Sprintf(
		"You are a product manager scoping a request against this codebase before any work starts.\n\n"+
			"Request: %s\n%s\n\n"+
			"Read enough of the repository to answer concretely, then write a short brief with these sections:\n"+
			"- What the user is actually asking for (one sentence)\n"+
			"- Where this lives in this codebase (real file paths)\n"+
			"- The change, described so a developer could start\n"+
			"- Open questions that need the requester's answer\n"+
			"- What is explicitly out of scope\n\n"+
			"Be concrete about this repository. Do not write the code.",
		title, body)
	return r.Run(ctx, q, Opts{Dir: dir, Timeout: 240 * time.Second, Model: "sonnet"})
}

// Title writes a short label for a queued message, so a stack of prompts is
// readable at a glance.
func (r *Runner) Title(ctx context.Context, text string) (string, error) {
	if len(text) > 2000 {
		text = text[:2000]
	}
	q := "Write a label of at most 6 words for this task. Output the label only.\n\n" + text
	out, err := r.Run(ctx, q, Opts{Timeout: 40 * time.Second})
	if err != nil {
		return "", err
	}
	return cleanOneLiner(out), nil
}

// DiagnoseLaunch explains why an agent CLI died at launch. Deterministic causes
// are answered by the caller without an AI call; this is the fallback for the
// ones that need reading.
func (r *Runner) DiagnoseLaunch(ctx context.Context, command, output string) (string, error) {
	if len(output) > 8000 {
		output = output[len(output)-8000:]
	}
	q := fmt.Sprintf(
		"An agent CLI failed to start. Explain in plain language what broke and give the ordered steps to fix it. "+
			"Be brief and specific. Do not suggest running anything destructive.\n\nCommand:\n%s\n\nOutput:\n%s",
		command, output)
	return r.Run(ctx, q, Opts{Timeout: 60 * time.Second})
}
