// A stand-in for the CLI, so an agent's environment can be inspected without a
// real account, a real key or a real request.
//
// Go AI Team spawns the agent binary named by the claudeBin setting. Pointing
// that at this writes the environment the child actually received to the file
// named by ENVDUMP_OUT and then waits, so the session is still alive while the
// API is asked what endpoint it thinks the agent is on.
//
// Build it where scratchpad/byok_check.py expects:
//
//	go build -o scratchpad/envdump/envdump.exe ./scratchpad/envdump
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func main() {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	if out := os.Getenv("ENVDUMP_OUT"); out != "" {
		b, _ := json.MarshalIndent(env, "", "  ")
		_ = os.WriteFile(out, b, 0o600)
	}
	// Something on screen, so the session looks alive rather than crashed.
	fmt.Println("envdump: wrote the environment, holding the terminal open")

	// A status line, for the check that reads one back. The CLI prints this at
	// the bottom of its own screen; here it is one line, which is all the
	// scrape needs.
	if line := os.Getenv("ENVDUMP_STATUSLINE"); line != "" {
		fmt.Println(line)
	}
	// Then a wall of output, which is what pushes a real status line out of the
	// window the parser reads. This is the condition the hold exists for.
	if n, _ := strconv.Atoi(os.Getenv("ENVDUMP_NOISE")); n > 0 {
		if wait, _ := strconv.Atoi(os.Getenv("ENVDUMP_NOISE_DELAY")); wait > 0 {
			time.Sleep(time.Duration(wait) * time.Second)
		}
		row := strings.Repeat("x", 79)
		for written := 0; written < n; written += 80 {
			fmt.Println(row)
		}
		fmt.Println("envdump: done being noisy")
	}
	time.Sleep(90 * time.Second)
}
