// Package guard finds the child processes an agent left behind.
//
// Every tool call an agent makes spawns a real process: a search, a compiler, a
// script. Most finish in seconds. Some never do, and those keep holding memory
// at zero percent CPU until the machine starts swapping — the worst kind of
// failure, because nothing appears to be wrong.
//
// The hard part is not finding processes, it is not crying wolf. A real build
// is large and busy; a stuck process is large, old AND idle. All three
// conditions must hold before anything is flagged, and nothing is ever killed
// without being asked for.
package guard

import (
	"context"
	"sort"
	"time"
)

// Thresholds for flagging. Chosen so an ordinary build never trips them: a
// compile is busy on the CPU, and a short-lived search is not old enough.
const (
	MinAge    = 4 * time.Minute
	MinMemory = 250 * 1024 * 1024 // 250 MB
	MaxCPU    = 2.0               // percent, averaged since we last looked
)

// Proc is one process in an agent's tree.
type Proc struct {
	PID     int       `json:"pid"`
	PPID    int       `json:"ppid"`
	Name    string    `json:"name"`
	CmdLine string    `json:"cmdLine,omitempty"`
	MemoryB int64     `json:"memoryBytes"`
	CPUPct  float64   `json:"cpuPct"`
	Started time.Time `json:"started"`
	AgeSecs int       `json:"ageSecs"`

	// Suspect is true when the process is large, old and idle at once.
	Suspect bool   `json:"suspect"`
	Reason  string `json:"reason,omitempty"`

	// AgentID and AgentName say which agent's tool call started this.
	AgentID   string `json:"agentId,omitempty"`
	AgentName string `json:"agentName,omitempty"`
	// Orphaned means the agent that started it is gone but the process is not.
	Orphaned bool `json:"orphaned"`
}

// Report is one sweep.
type Report struct {
	At          time.Time `json:"at"`
	Procs       []Proc    `json:"procs"`
	Suspects    int       `json:"suspects"`
	HeldBytes   int64     `json:"heldBytes"`
	Supported   bool      `json:"supported"`
	Unsupported string    `json:"unsupported,omitempty"`
}

// Root is one agent's process tree to sweep.
type Root struct {
	PID       int
	AgentID   string
	AgentName string
	// Alive is false when the agent has exited but its children may not have.
	Alive bool
}

// protectedNames are never flagged. The agent CLI itself and the shell are
// long-lived by design, and a dev server is supposed to sit idle waiting for a
// request — flagging those would make the feature useless noise.
var protectedNames = map[string]bool{
	"claude":         true,
	"claude.exe":     true,
	"codex":          true,
	"codex.exe":      true,
	"grok":           true,
	"grok.exe":       true,
	"bash":           true,
	"bash.exe":       true,
	"sh":             true,
	"zsh":            true,
	"cmd.exe":        true,
	"powershell.exe": true,
	"pwsh.exe":       true,
	"conhost.exe":    true,
	"go-ai-team":     true,
	"go-ai-team.exe": true,
}

// Sweep walks the descendants of every root and flags the suspects.
func Sweep(ctx context.Context, roots []Root) Report {
	rep := Report{At: time.Now()}

	all, err := listProcesses(ctx)
	if err != nil {
		rep.Unsupported = err.Error()
		return rep
	}
	rep.Supported = true

	// Index by parent so descendants can be walked without rescanning.
	children := map[int][]*Proc{}
	byPID := map[int]*Proc{}
	for i := range all {
		p := &all[i]
		byPID[p.PID] = p
		children[p.PPID] = append(children[p.PPID], p)
	}

	seen := map[int]bool{}
	var out []Proc
	for _, root := range roots {
		// Walk breadth-first from the agent's own process.
		queue := []int{root.PID}
		for len(queue) > 0 {
			pid := queue[0]
			queue = queue[1:]
			for _, c := range children[pid] {
				if seen[c.PID] {
					continue
				}
				seen[c.PID] = true
				queue = append(queue, c.PID)

				p := *c
				p.AgentID = root.AgentID
				p.AgentName = root.AgentName
				p.Orphaned = !root.Alive
				p.AgeSecs = int(time.Since(p.Started).Seconds())
				classify(&p)
				if p.Suspect {
					rep.Suspects++
					rep.HeldBytes += p.MemoryB
				}
				out = append(out, p)
			}
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Suspect != out[j].Suspect {
			return out[i].Suspect
		}
		return out[i].MemoryB > out[j].MemoryB
	})
	rep.Procs = out
	return rep
}

// classify decides whether one process is stuck, and says why.
func classify(p *Proc) {
	if protectedNames[p.Name] {
		return
	}
	if p.Started.IsZero() {
		// Without a start time we cannot judge age, and guessing would risk a
		// false positive on a brand-new process.
		return
	}
	age := time.Since(p.Started)
	oldEnough := age >= MinAge
	bigEnough := p.MemoryB >= MinMemory
	idle := p.CPUPct <= MaxCPU

	if oldEnough && bigEnough && idle {
		p.Suspect = true
		p.Reason = "holding " + humanBytes(p.MemoryB) + " at " +
			trimFloat(p.CPUPct) + "% CPU for " + humanDuration(age)
		if p.Orphaned {
			p.Reason += ", and its agent has already exited"
		}
	}
}

func humanBytes(b int64) string {
	const u = 1024
	if b < u {
		return itoa(b) + " B"
	}
	units := []string{"KB", "MB", "GB", "TB"}
	f := float64(b)
	i := -1
	for f >= u && i < len(units)-1 {
		f /= u
		i++
	}
	return trimFloat(f) + " " + units[i]
}

func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return itoa(int64(d.Seconds())) + "s"
	case d < time.Hour:
		return itoa(int64(d.Minutes())) + " min"
	default:
		return trimFloat(d.Hours()) + " h"
	}
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [24]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// trimFloat renders one decimal place, dropping a trailing ".0".
func trimFloat(f float64) string {
	v := int64(f*10 + 0.5)
	whole, frac := v/10, v%10
	if frac == 0 {
		return itoa(whole)
	}
	return itoa(whole) + "." + itoa(frac)
}
