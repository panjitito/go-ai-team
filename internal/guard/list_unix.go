//go:build !windows

package guard

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// listProcesses reads the process table with ps, which exists on both macOS and
// Linux with the same column flags.
//
// rss is the resident set, so unlike the Windows path this does not see pages
// that have been swapped out. It is the best portable figure available; the
// age-and-idle half of the test is what carries the accuracy here.
func listProcesses(ctx context.Context) ([]Proc, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ps", "-eo", "pid=,ppid=,rss=,pcpu=,etimes=,comm=,args=")
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("could not read the process table: %s", msg)
	}

	now := time.Now()
	var procs []Proc
	for _, line := range strings.Split(out.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Six fixed numeric/short columns, then args, which may contain spaces.
		f := strings.Fields(line)
		if len(f) < 6 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		rssKB, err3 := strconv.ParseInt(f[2], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		cpu, _ := strconv.ParseFloat(f[3], 64)
		etimes, _ := strconv.ParseInt(f[4], 10, 64)
		name := f[5]

		args := ""
		if len(f) > 6 {
			args = strings.Join(f[6:], " ")
		}

		p := Proc{
			PID:     pid,
			PPID:    ppid,
			Name:    trimBase(name),
			CmdLine: trimCmd(args),
			MemoryB: rssKB * 1024,
			CPUPct:  cpu,
		}
		if etimes > 0 {
			p.Started = now.Add(-time.Duration(etimes) * time.Second)
		}
		procs = append(procs, p)
	}
	return procs, nil
}

// trimBase reduces a path to its executable name so protectedNames matches.
func trimBase(s string) string {
	if i := strings.LastIndexAny(s, "/\\"); i >= 0 {
		return s[i+1:]
	}
	return s
}

func trimCmd(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}
