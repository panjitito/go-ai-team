// Package browser opens the UI in a Chrome profile that belongs to this app.
//
// The whole product bet is that a browser is a better shell than Electron. That
// only holds if the window behaves like an application rather than like a tab:
// its own icon in the taskbar, no omnibox, no bookmark bar, and — most
// importantly — its own profile, so the app does not inherit whatever
// extensions, sessions and cookies live in the browsing profile.
//
// A dedicated --user-data-dir gives all of that for free. It is a real, separate
// Chrome profile living under ~/.goaiteam/browser, and nothing in it touches the
// user's normal browsing.
package browser

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Mode says how to open the UI.
type Mode string

const (
	// ModeApp opens a chromeless window in the app's own profile.
	ModeApp Mode = "app"
	// ModeTab opens an ordinary tab in the app's own profile.
	ModeTab Mode = "tab"
	// ModeSystem hands the URL to whatever browser the OS prefers, in the
	// user's normal profile.
	ModeSystem Mode = "system"
	// ModeNone opens nothing.
	ModeNone Mode = "none"
)

// Browser is a located Chrome-family executable.
type Browser struct {
	Name string
	Path string
}

// candidates lists the Chrome-family browsers we can drive, best first. They all
// accept the same --user-data-dir and --app flags, so any of them gives the same
// behaviour; Chrome is simply the most likely to be present and current.
func candidates() []Browser {
	switch runtime.GOOS {
	case "windows":
		local := os.Getenv("LOCALAPPDATA")
		pf := os.Getenv("ProgramFiles")
		pf86 := os.Getenv("ProgramFiles(x86)")
		return []Browser{
			{"Chrome", filepath.Join(pf, `Google\Chrome\Application\chrome.exe`)},
			{"Chrome", filepath.Join(pf86, `Google\Chrome\Application\chrome.exe`)},
			{"Chrome", filepath.Join(local, `Google\Chrome\Application\chrome.exe`)},
			{"Edge", filepath.Join(pf86, `Microsoft\Edge\Application\msedge.exe`)},
			{"Edge", filepath.Join(pf, `Microsoft\Edge\Application\msedge.exe`)},
			{"Brave", filepath.Join(pf, `BraveSoftware\Brave-Browser\Application\brave.exe`)},
			{"Vivaldi", filepath.Join(local, `Vivaldi\Application\vivaldi.exe`)},
		}
	case "darwin":
		return []Browser{
			{"Chrome", "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"},
			{"Edge", "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge"},
			{"Brave", "/Applications/Brave Browser.app/Contents/MacOS/Brave Browser"},
			{"Chromium", "/Applications/Chromium.app/Contents/MacOS/Chromium"},
		}
	default:
		return []Browser{
			{"Chrome", "google-chrome"},
			{"Chrome", "google-chrome-stable"},
			{"Chromium", "chromium"},
			{"Chromium", "chromium-browser"},
			{"Brave", "brave-browser"},
			{"Edge", "microsoft-edge"},
		}
	}
}

// Find locates a usable Chrome-family browser.
func Find() (Browser, bool) {
	for _, c := range candidates() {
		if c.Path == "" {
			continue
		}
		// An absolute path is checked directly; a bare name is looked up on PATH.
		if filepath.IsAbs(c.Path) {
			if st, err := os.Stat(c.Path); err == nil && !st.IsDir() {
				return c, true
			}
			continue
		}
		if p, err := exec.LookPath(c.Path); err == nil {
			c.Path = p
			return c, true
		}
	}
	return Browser{}, false
}

// Opts configures a launch.
type Opts struct {
	URL string
	// ProfileDir is the dedicated --user-data-dir. Created if missing.
	ProfileDir string
	Mode       Mode
	Width      int
	Height     int
}

// Open launches the UI and returns a short description of what it did, so the
// startup banner can tell the truth rather than assume.
func Open(o Opts) (string, error) {
	switch o.Mode {
	case ModeNone:
		return "", nil
	case ModeSystem:
		if err := openSystem(o.URL); err != nil {
			return "", err
		}
		return "opened in your default browser", nil
	}

	b, ok := Find()
	if !ok {
		// No Chrome-family browser: fall back rather than refusing to open
		// anything, but say which happened.
		if err := openSystem(o.URL); err != nil {
			return "", err
		}
		return "no Chrome-family browser found, so it opened in your default browser", nil
	}

	if err := prepareProfile(o.ProfileDir); err != nil {
		return "", fmt.Errorf("could not prepare the browser profile: %w", err)
	}

	w, h := o.Width, o.Height
	if w <= 0 {
		w = 1440
	}
	if h <= 0 {
		h = 940
	}

	args := []string{
		"--user-data-dir=" + o.ProfileDir,
		// This profile exists only for this app, so the usual first-run
		// interruptions are noise: there is nothing to import and no decision
		// to make about default browsers.
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-search-engine-choice-screen",
		fmt.Sprintf("--window-size=%d,%d", w, h),
	}
	if o.Mode == ModeApp {
		// --app gives a window with no omnibox, no bookmark bar and its own
		// taskbar entry, which is what makes it read as an application.
		args = append(args, "--app="+o.URL)
	} else {
		args = append(args, "--new-window", o.URL)
	}

	cmd := exec.Command(b.Path, args...)
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("could not start %s: %w", b.Name, err)
	}
	// Reap in the background so the browser is not left a zombie child, and so
	// closing the window does not block our shutdown.
	go func() { _ = cmd.Wait() }()

	what := "an app window"
	if o.Mode == ModeTab {
		what = "a tab"
	}
	return fmt.Sprintf("opened %s in a dedicated %s profile", what, b.Name), nil
}

// chromePrefs is the subset of Chrome's Preferences file worth seeding.
type chromePrefs map[string]any

// prepareProfile creates the profile directory and seeds preferences.
//
// Two of these matter in practice. exit_type suppresses the "Chrome didn't shut
// down correctly / Restore pages?" bubble, which otherwise appears every time
// the app window is closed by quitting rather than by the window button — and an
// app window is closed that way constantly. The bookmark bar is hidden because
// an application window showing someone's bookmarks looks broken.
func prepareProfile(dir string) error {
	if dir == "" {
		return fmt.Errorf("no profile directory given")
	}
	defaultDir := filepath.Join(dir, "Default")
	if err := os.MkdirAll(defaultDir, 0o700); err != nil {
		return err
	}

	prefsPath := filepath.Join(defaultDir, "Preferences")
	prefs := chromePrefs{}
	if b, err := os.ReadFile(prefsPath); err == nil {
		// Merge rather than overwrite: the user may have changed things like
		// zoom or the theme inside this profile, and those should survive.
		_ = json.Unmarshal(b, &prefs)
	}

	setPath(prefs, []string{"profile", "exit_type"}, "Normal")
	setPath(prefs, []string{"profile", "exited_cleanly"}, true)
	setPath(prefs, []string{"profile", "name"}, "Go AI Team")
	setPath(prefs, []string{"bookmark_bar", "show_on_all_tabs"}, false)
	setPath(prefs, []string{"browser", "check_default_browser"}, false)
	setPath(prefs, []string{"browser", "custom_chrome_frame"}, false)
	// The app is served over plain HTTP on loopback, which browsers already
	// treat as a secure context, so nothing here relaxes a security setting.
	setPath(prefs, []string{"credentials_enable_service"}, false)
	setPath(prefs, []string{"profile", "password_manager_enabled"}, false)

	b, err := json.Marshal(prefs)
	if err != nil {
		return err
	}
	tmp := prefsPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, prefsPath); err != nil {
		return err
	}

	// A README in the profile explains what the directory is, because an
	// unexplained multi-hundred-megabyte Chrome profile appearing in a dotfolder
	// is exactly the kind of thing that gets deleted in confusion later.
	readme := filepath.Join(dir, "README.txt")
	if _, err := os.Stat(readme); os.IsNotExist(err) {
		_ = os.WriteFile(readme, []byte(
			"This is a Chrome profile used only by Go AI Team.\n\n"+
				"It exists so the app window has its own cookies, extensions and history,\n"+
				"separate from your normal browsing. Nothing here touches your main profile.\n\n"+
				"Deleting this folder is safe: it is recreated on the next launch, and the\n"+
				"only thing lost is the app window's own local storage.\n"), 0o600)
	}
	return nil
}

// setPath writes a nested key, creating intermediate maps.
func setPath(m map[string]any, path []string, val any) {
	cur := m
	for i, k := range path {
		if i == len(path)-1 {
			cur[k] = val
			return
		}
		next, ok := cur[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[k] = next
		}
		cur = next
	}
}

// openSystem hands the URL to the operating system's default handler.
func openSystem(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// ParseMode reads the --browser flag.
func ParseMode(s string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "app", "":
		return ModeApp, nil
	case "tab":
		return ModeTab, nil
	case "system", "default":
		return ModeSystem, nil
	case "none", "off", "false":
		return ModeNone, nil
	}
	return "", fmt.Errorf("unknown browser mode %q; use app, tab, system or none", s)
}
