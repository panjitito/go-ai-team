package browser

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// A dedicated profile is only half of "it behaves like an app": the other half
// is being able to start it without a terminal. This writes a launcher that
// starts the server and opens the app window in one double-click.
//
// It is deliberately opt-in behind a flag rather than something that happens on
// first run, because writing to somebody's desktop or Start menu uninvited is
// exactly the sort of thing a tool should ask about first.

// Shortcut describes what was created.
type Shortcut struct {
	Path string
	Note string
}

// InstallShortcut creates a desktop launcher for the app.
func InstallShortcut(exePath string, port int) (Shortcut, error) {
	abs, err := filepath.Abs(exePath)
	if err != nil {
		return Shortcut{}, err
	}
	if _, err := os.Stat(abs); err != nil {
		return Shortcut{}, fmt.Errorf("cannot find the executable at %s", abs)
	}

	switch runtime.GOOS {
	case "windows":
		return installWindows(abs, port)
	case "darwin":
		return installDarwin(abs, port)
	default:
		return installLinux(abs, port)
	}
}

func desktopDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	// OneDrive-redirected desktops are common on Windows, so prefer one that
	// actually exists over the nominal path.
	for _, c := range []string{
		filepath.Join(home, "OneDrive", "Desktop"),
		filepath.Join(home, "Desktop"),
	} {
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			return c, nil
		}
	}
	return home, nil
}

// installWindows writes a .lnk through PowerShell's COM shell, which is the
// only way to produce a real shortcut without shipping a Windows API binding
// for something this peripheral.
func installWindows(exe string, port int) (Shortcut, error) {
	desk, err := desktopDir()
	if err != nil {
		return Shortcut{}, err
	}
	lnk := filepath.Join(desk, "Go AI Team.lnk")

	args := ""
	if port != 0 {
		args = fmt.Sprintf("--port %d", port)
	}
	// Quoting matters here: paths routinely contain spaces, and a broken
	// shortcut fails in a way that is annoying to diagnose.
	script := fmt.Sprintf(`
$ErrorActionPreference='Stop'
$s = (New-Object -ComObject WScript.Shell).CreateShortcut(%s)
$s.TargetPath = %s
$s.Arguments = %s
$s.WorkingDirectory = %s
$s.Description = 'Go AI Team — multi-account cockpit for Claude Code'
$s.IconLocation = %s
$s.Save()
`, psQuote(lnk), psQuote(exe), psQuote(args), psQuote(filepath.Dir(exe)), psQuote(exe+",0"))

	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		return Shortcut{}, fmt.Errorf("could not create the shortcut: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return Shortcut{
		Path: lnk,
		Note: "double-click it to start the server and open the app window",
	}, nil
}

// psQuote renders a Go string as a PowerShell single-quoted literal.
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func installDarwin(exe string, port int) (Shortcut, error) {
	desk, err := desktopDir()
	if err != nil {
		return Shortcut{}, err
	}
	path := filepath.Join(desk, "Go AI Team.command")
	arg := ""
	if port != 0 {
		arg = fmt.Sprintf(" --port %d", port)
	}
	body := fmt.Sprintf("#!/bin/sh\nexec %q%s\n", exe, arg)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		return Shortcut{}, err
	}
	return Shortcut{Path: path, Note: "double-click it to start the app"}, nil
}

func installLinux(exe string, port int) (Shortcut, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Shortcut{}, err
	}
	dir := filepath.Join(home, ".local", "share", "applications")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Shortcut{}, err
	}
	path := filepath.Join(dir, "go-ai-team.desktop")
	arg := ""
	if port != 0 {
		arg = fmt.Sprintf(" --port %d", port)
	}
	body := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=Go AI Team
Comment=Multi-account cockpit for Claude Code
Exec=%s%s
Terminal=true
Categories=Development;
`, exe, arg)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return Shortcut{}, err
	}
	return Shortcut{Path: path, Note: "it will appear in your applications menu"}, nil
}
