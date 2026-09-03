//go:build windows

package guard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// listProcesses reads the process table through CIM.
//
// WorkingSetSize is deliberately not the number reported: Windows reports a
// process's working set, but a stuck process can show 20 MB of working set
// while holding gigabytes that have been paged out. PrivatePageCount counts the
// committed private pages including what was swapped, which is the footprint
// that actually matters when the machine starts thrashing.
func listProcesses(ctx context.Context) ([]Proc, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()

	const script = `
$ErrorActionPreference='Stop'
Get-CimInstance Win32_Process |
  Select-Object ProcessId,ParentProcessId,Name,CommandLine,PrivatePageCount,WorkingSetSize,KernelModeTime,UserModeTime,CreationDate |
  ConvertTo-Json -Compress -Depth 2
`
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", script)
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

	raw := bytes.TrimSpace(out.Bytes())
	if len(raw) == 0 {
		return nil, fmt.Errorf("the process table came back empty")
	}
	// A single process would serialise as an object rather than an array.
	if raw[0] == '{' {
		raw = append(append([]byte{'['}, raw...), ']')
	}

	type row struct {
		ProcessId        int    `json:"ProcessId"`
		ParentProcessId  int    `json:"ParentProcessId"`
		Name             string `json:"Name"`
		CommandLine      string `json:"CommandLine"`
		PrivatePageCount int64  `json:"PrivatePageCount"`
		WorkingSetSize   int64  `json:"WorkingSetSize"`
		KernelModeTime   int64  `json:"KernelModeTime"`
		UserModeTime     int64  `json:"UserModeTime"`
		CreationDate     any    `json:"CreationDate"`
	}
	var rows []row
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("could not parse the process table: %w", err)
	}

	now := time.Now()
	out2 := make([]Proc, 0, len(rows))
	for _, r := range rows {
		mem := r.PrivatePageCount
		if mem <= 0 {
			mem = r.WorkingSetSize
		}
		p := Proc{
			PID:     r.ProcessId,
			PPID:    r.ParentProcessId,
			Name:    r.Name,
			CmdLine: trimCmd(r.CommandLine),
			MemoryB: mem,
			Started: parseCimDate(r.CreationDate),
		}
		// Kernel and user time are in 100-nanosecond units. Total CPU time over
		// wall-clock lifetime is the average busyness — a stuck process shows
		// near zero here however much memory it holds.
		if !p.Started.IsZero() {
			life := now.Sub(p.Started).Seconds()
			if life > 1 {
				cpuSecs := float64(r.KernelModeTime+r.UserModeTime) / 1e7
				p.CPUPct = cpuSecs / life * 100
			}
		}
		out2 = append(out2, p)
	}
	return out2, nil
}

// parseCimDate handles the shapes ConvertTo-Json produces for a CIM datetime:
// an ISO string, or an object carrying epoch milliseconds.
func parseCimDate(v any) time.Time {
	switch t := v.(type) {
	case string:
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.999999999-07:00"} {
			if ts, err := time.Parse(layout, t); err == nil {
				return ts
			}
		}
		// PowerShell sometimes emits /Date(1699999999999)/.
		if strings.HasPrefix(t, "/Date(") {
			inner := strings.TrimSuffix(strings.TrimPrefix(t, "/Date("), ")/")
			if i := strings.IndexAny(inner, "+-"); i > 0 {
				inner = inner[:i]
			}
			var ms int64
			if _, err := fmt.Sscanf(inner, "%d", &ms); err == nil && ms > 0 {
				return time.UnixMilli(ms)
			}
		}
	case map[string]any:
		for _, k := range []string{"DateTime", "value"} {
			if s, ok := t[k].(string); ok {
				return parseCimDate(s)
			}
		}
	}
	return time.Time{}
}

func trimCmd(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}
