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
	time.Sleep(90 * time.Second)
}
