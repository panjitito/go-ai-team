//go:build uitest

package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tool cards rendered as the thing each tool is.
//
// The shapes below are copied from real transcripts on this machine, which is
// the point: an Edit really does carry old_string and new_string with escaped
// newlines, AskUserQuestion really does record its answers in one sentence at
// the end of the result, and Read really does return its own line numbers as
// text. Every one of those used to be shown as raw JSON.
func TestUIToolView(t *testing.T) {
	const port = 7819
	startServer(t, port)

	c := launchChrome(t)
	c.openTarget(t, fmt.Sprintf("http://127.0.0.1:%d/", port))

	if ready := c.evalString(t, `
new Promise(async resolve => {
  // Waiting for the tab bar waits for the app's own first render, which would
  // otherwise wipe what is drawn into #main below.
  for (let i = 0; i < 80; i++) {
    if (typeof toolBody === 'function' && document.querySelectorAll('#tabbar .tab').length) {
      await new Promise(r => setTimeout(r, 500));
      return resolve('ready');
    }
    await new Promise(r => setTimeout(r, 250));
  }
  resolve('timeout');
})`); ready != "ready" {
		t.Fatalf("the UI never finished loading: %s", ready)
	}

	got := c.evalString(t, `
new Promise(async resolve => {
  try {
    const host = document.querySelector('#main');
    host.innerHTML = '';
    const draw = t => {
      const n = toolEl(Object.assign({ id: 't' + Math.random(), result: '', pending: false }, t), new Set());
      n.classList.add('open');
      host.append(n);
      return n;
    };
    const out = {};

    // --- Bash: the command as a command, the output as output ---------------
    let n = draw({
      name: 'Bash', summary: 'Inspect the schema',
      input: JSON.stringify({ command: 'mysql -e "DESCRIBE t"', description: 'Inspect the schema' }),
      result: 'Field\tType\nid\tbigint',
    });
    out.bashCmd = (n.querySelector('.tv-cmd') || {}).textContent || '';
    out.bashOut = !!n.querySelector('.tool-pre');

    // --- Edit: a diff, not a wall of escaped newlines -----------------------
    n = draw({
      name: 'Edit',
      input: JSON.stringify({
        file_path: 'C:/proj/config/mis.php',
        old_string: 'return [\n    | Server metrics\n    | keep\n];',
        new_string: 'return [\n    | Server health\n    | keep\n];',
      }),
      result: 'The file has been updated successfully.',
    });
    out.diffRows = n.querySelectorAll('.tv-row').length;
    out.dels = [...n.querySelectorAll('.tv-row.del .tv-text')].map(x => x.textContent);
    out.adds = [...n.querySelectorAll('.tv-row.add .tv-text')].map(x => x.textContent);
    out.ctx = n.querySelectorAll('.tv-row.ctx').length;
    out.editHasJson = (n.textContent || '').includes('old_string');
    out.rawAvailable = !!n.querySelector('.tv-raw');

    // --- Write: the file, not a JSON string containing the file -------------
    n = draw({
      name: 'Write',
      input: JSON.stringify({ file_path: 'C:/proj/x.php', content: '<?php\necho 1;\n' }),
      result: 'File created successfully at: C:/proj/x.php',
    });
    out.writeShowsCode = !!n.querySelector('.code-body');
    out.writeHasJson = (n.textContent || '').includes('"content"');

    // --- Read: its line numbers become a gutter -----------------------------
    n = draw({
      name: 'Read',
      input: JSON.stringify({ file_path: 'C:/proj/notes.txt' }),
      result: '1\tGithub URL: https://example.test\n2\tsecond line',
    });
    out.readGutter = [...n.querySelectorAll('.tv-num .code-gutter div')].map(x => x.textContent);
    out.readBody = [...n.querySelectorAll('.tv-num .code-body div')].map(x => x.textContent);

    // --- AskUserQuestion: what was asked, and what was chosen ---------------
    n = draw({
      name: 'AskUserQuestion',
      input: JSON.stringify({ questions: [{
        header: 'Browser', question: 'Which browser should I use?',
        options: [
          { label: 'Browser 1 (Windows)', description: 'deviceId: aaa' },
          { label: 'Browser 2 (Windows)', description: 'deviceId: bbb' },
        ],
      }]}),
      result: 'Your questions have been answered: "Which browser should I use?"="Browser 2 (Windows)". You can now continue.',
    });
    out.askQuestion = (n.querySelector('.tv-q strong') || {}).textContent || '';
    out.askHeader = (n.querySelector('.tv-q .pill') || {}).textContent || '';
    out.askOpts = n.querySelectorAll('.tv-opt').length;
    out.askChosen = [...n.querySelectorAll('.tv-opt.chosen .tv-opt-label')].map(x => x.textContent);
    out.askHasJson = (n.textContent || '').includes('"questions"');

    // --- an MCP tool: the server named separately ---------------------------
    n = draw({
      name: 'mcp__MSSQL_ReSM__query_ReSM',
      input: JSON.stringify({ sql: 'select 1' }),
      result: '1',
    });
    out.mcpName = (n.querySelector('.tool-name') || {}).textContent || '';
    out.mcpVia = (n.querySelector('.tool-via') || {}).textContent || '';

    // --- an unknown tool: fields, not a JSON dump ---------------------------
    n = draw({
      name: 'SomethingNew',
      input: JSON.stringify({ alpha: 'one', beta: 42 }),
      result: 'ok',
    });
    out.fieldKeys = [...n.querySelectorAll('.tv-key')].map(x => x.textContent);
    // The value belongs next to its name, not at the far edge of the window.
    {
      const cells = n.querySelectorAll('.tv-fields tr:first-child td');
      const gap = cells[1].getBoundingClientRect().left - cells[0].getBoundingClientRect().right;
      out.fieldGap = Math.round(gap);
    }

    // --- a malformed input must not lose the card ---------------------------
    n = draw({ name: 'Edit', input: 'not json at all', result: 'ok' });
    out.brokenStillRenders = !!n.querySelector('.tool-pre');

    resolve(JSON.stringify(out));
  } catch (e) {
    resolve(JSON.stringify({ threw: String(e && e.stack || e) }));
  }
})`)
	t.Logf("toolview: %s", got)
	if strings.Contains(got, `"threw"`) {
		t.Fatalf("the page threw: %s", got)
	}

	if dir := os.Getenv("UISHOT_DIR"); dir != "" {
		res := c.call(t, "Page.captureScreenshot", map[string]any{"format": "png"})
		if data, _ := res["data"].(string); data != "" {
			if raw, err := base64.StdEncoding.DecodeString(data); err == nil {
				_ = os.WriteFile(filepath.Join(dir, "toolview.png"), raw, 0o644)
			}
		}
	}

	var r struct {
		BashCmd            string   `json:"bashCmd"`
		BashOut            bool     `json:"bashOut"`
		DiffRows           int      `json:"diffRows"`
		Dels               []string `json:"dels"`
		Adds               []string `json:"adds"`
		Ctx                int      `json:"ctx"`
		EditHasJSON        bool     `json:"editHasJson"`
		RawAvailable       bool     `json:"rawAvailable"`
		WriteShowsCode     bool     `json:"writeShowsCode"`
		WriteHasJSON       bool     `json:"writeHasJson"`
		ReadGutter         []string `json:"readGutter"`
		ReadBody           []string `json:"readBody"`
		AskQuestion        string   `json:"askQuestion"`
		AskHeader          string   `json:"askHeader"`
		AskOpts            int      `json:"askOpts"`
		AskChosen          []string `json:"askChosen"`
		AskHasJSON         bool     `json:"askHasJson"`
		MCPName            string   `json:"mcpName"`
		MCPVia             string   `json:"mcpVia"`
		FieldKeys          []string `json:"fieldKeys"`
		FieldGap           int      `json:"fieldGap"`
		BrokenStillRenders bool     `json:"brokenStillRenders"`
	}
	if err := json.Unmarshal([]byte(got), &r); err != nil {
		t.Fatalf("unreadable: %v (%s)", err, got)
	}

	if !strings.Contains(r.BashCmd, "$ mysql") {
		t.Errorf("the command is not shown as one: %q", r.BashCmd)
	}
	if !r.BashOut {
		t.Error("the command produced output and none is shown")
	}

	// The heart of it: one line changed, so one line goes and one arrives, and
	// the lines around it stay as context.
	if len(r.Dels) != 1 || !strings.Contains(r.Dels[0], "Server metrics") {
		t.Errorf("removed lines = %v, want just the metrics line", r.Dels)
	}
	if len(r.Adds) != 1 || !strings.Contains(r.Adds[0], "Server health") {
		t.Errorf("added lines = %v, want just the health line", r.Adds)
	}
	if r.Ctx != 3 {
		t.Errorf("%d context lines, want the 3 that did not change", r.Ctx)
	}
	if r.EditHasJSON {
		t.Error("the edit is still showing its JSON input")
	}
	if !r.RawAvailable {
		t.Error("the untouched result is not reachable; a rendering is an interpretation")
	}

	if !r.WriteShowsCode || r.WriteHasJSON {
		t.Errorf("write: code=%v json=%v", r.WriteShowsCode, r.WriteHasJSON)
	}

	if len(r.ReadGutter) != 2 || r.ReadGutter[0] != "1" || r.ReadGutter[1] != "2" {
		t.Errorf("read gutter = %v, want the line numbers", r.ReadGutter)
	}
	if len(r.ReadBody) != 2 || strings.HasPrefix(r.ReadBody[0], "1\t") {
		t.Errorf("read body = %v — the number should be the gutter, not the text", r.ReadBody)
	}

	if r.AskQuestion != "Which browser should I use?" {
		t.Errorf("question = %q", r.AskQuestion)
	}
	if r.AskHeader != "Browser" {
		t.Errorf("header = %q", r.AskHeader)
	}
	if r.AskOpts != 2 {
		t.Errorf("%d options, want 2", r.AskOpts)
	}
	if len(r.AskChosen) != 1 || r.AskChosen[0] != "Browser 2 (Windows)" {
		t.Errorf("chosen = %v, want the one the result records", r.AskChosen)
	}
	if r.AskHasJSON {
		t.Error("the question is still showing its JSON input — the exact thing that prompted this")
	}

	if r.MCPName != "query_ReSM" || r.MCPVia != "MSSQL_ReSM" {
		t.Errorf("mcp name = %q via %q", r.MCPName, r.MCPVia)
	}
	if len(r.FieldKeys) != 2 {
		t.Errorf("unknown tool fields = %v, want its keys listed", r.FieldKeys)
	}
	if r.FieldGap > 40 {
		t.Errorf("a value sits %dpx from its name — it should read as a pair", r.FieldGap)
	}
	if !r.BrokenStillRenders {
		t.Error("a tool whose input will not parse lost its card entirely")
	}
}
