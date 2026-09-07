//go:build uitest

package server

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// The pictures in the README.
//
// Taken from the real UI in the tests' own headless Chrome, driven with made-up
// state: invented projects, invented agents, an invented conversation. Nobody's
// actual work goes into a public README, and a screenshot of an empty app sells
// nothing — so the app is given something worth showing and photographed doing
// it.
//
//	go test -tags uitest ./internal/server/ -run TestUIShots
//
// Skipped unless SHOTS_DIR says where to put them, because it writes files.
func TestUIShots(t *testing.T) {
	dir := os.Getenv("SHOTS_DIR")
	if dir == "" {
		t.Skip("set SHOTS_DIR to regenerate the README screenshots")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	const port = 7835
	startServer(t, port)

	c := launchChrome(t)
	c.openTarget(t, fmt.Sprintf("http://127.0.0.1:%d/", port))

	// Twice the pixels, so the images stay sharp on the displays people read
	// GitHub on. The height is set per picture: a screenshot that is half empty
	// space reads as an empty app.
	view := func(h int) {
		c.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": 1360, "height": h, "deviceScaleFactor": 2, "mobile": false,
		})
	}
	view(850)

	if ready := c.evalString(t, `
new Promise(async resolve => {
  for (let i = 0; i < 80; i++) {
    if (typeof renderConversation === 'function' && document.querySelectorAll('#tabbar .tab').length) {
      await new Promise(r => setTimeout(r, 500));
      return resolve('ready');
    }
    await new Promise(r => setTimeout(r, 250));
  }
  resolve('timeout');
})`); ready != "ready" {
		t.Fatalf("the UI never finished loading: %s", ready)
	}

	// The world these pictures are of. Invented, and plausible: a few projects,
	// a row of agents in every state the board can show, on two accounts.
	if out := c.evalString(t, `
new Promise(async resolve => {
  try {
    S.accounts = [
      { id: 'ac1', name: 'work',     provider: 'claude', color: '#3b82f6', signedIn: true },
      { id: 'ac2', name: 'personal', provider: 'claude', color: '#10b981', signedIn: true },
    ];
    S.folders = [];
    S.projects = [
      { id: 'p1', name: 'storefront',    path: 'C:\\code\\storefront',    accountId: 'ac1' },
      { id: 'p2', name: 'billing-api',   path: 'C:\\code\\billing-api',   accountId: 'ac1' },
      { id: 'p3', name: 'data-pipeline', path: 'C:\\code\\data-pipeline', accountId: 'ac2' },
    ];
    S.selectedProject = 'p1';
    S.agents = [
      { id: 'a1', projectId: 'p1', name: 'Checkout',   role: 'Backend',    color: '#8b5cf6', accountId: 'ac1', model: 'opus' },
      { id: 'a2', projectId: 'p1', name: 'Storefront', role: 'Frontend',   color: '#06b6d4', accountId: 'ac1', model: 'sonnet' },
      { id: 'a3', projectId: 'p1', name: 'Migrations', role: 'Backend',    color: '#8b5cf6', accountId: 'ac2', model: 'opus' },
      { id: 'a4', projectId: 'p1', name: 'Reviewer',   role: 'Reviewer',   color: '#ec4899', accountId: 'ac1', model: 'sonnet' },
      { id: 'a5', projectId: 'p1', name: 'Docs',       role: 'Docs',       color: '#64748b', accountId: 'ac2', model: 'haiku' },
      { id: 'a6', projectId: 'p1', name: 'Load tests', role: 'QA',         color: '#ef4444', accountId: 'ac1', model: 'sonnet' },
    ];
    const sess = (id, agentId, status, extra) => Object.assign({
      id, agentId, projectId: 'p1', kind: 'agent', status,
      accountName: agentId === 'a3' || agentId === 'a5' ? 'personal' : 'work',
      accountColor: agentId === 'a3' || agentId === 'a5' ? '#10b981' : '#3b82f6',
      switchLog: [], tokens: {}, totalTokens: 0, cacheHitRate: 0,
    }, extra || {});
    S.sessions = [
      sess('s1', 'a1', 'working',  { totalTokens: 184320, cacheHitRate: 71 }),
      sess('s2', 'a2', 'waiting',  { totalTokens: 96500,  cacheHitRate: 64,
              needsYou: true, question: 'Do you want to create src/checkout/Summary.tsx?' }),
      sess('s3', 'a3', 'working',  { totalTokens: 412900, cacheHitRate: 83 }),
      sess('s4', 'a4', 'waiting',  { totalTokens: 51200,  cacheHitRate: 58 }),
      sess('s6', 'a6', 'working',  { totalTokens: 22800,  cacheHitRate: 41 }),
    ];
    S.view = 'grid';
    render();
    await new Promise(r => setTimeout(r, 500));
    resolve('seeded');
  } catch (e) { resolve('threw: ' + e.message); }
})`); out != "seeded" {
		t.Fatalf("could not seed the demo state: %s", out)
	}
	view(500)
	c.evalString(t, `new Promise(r => setTimeout(() => r('ok'), 250))`)
	shot(t, c, dir, "grid.png")

	// The conversation: prose, a tool call as a diff, the plan above the
	// composer, and the changed-files rail beside it.
	if out := c.evalString(t, `
new Promise(async resolve => {
  try {
    openAgent('s1');
    await new Promise(r => setTimeout(r, 400));
    stopChatPoll();

    const now = Date.now();
    const when = m => new Date(now - m * 60000).toISOString();
    const d = {
      sessionId: 's1', status: 'working', ready: true,
      totalTokens: 184320, cacheHitRate: 71,
      line: 'opus · high effort · 5h window resets 16:40 · $2.14 this run',
      tasks: [
        { id: 't1', subject: 'Read the current checkout flow', status: 'completed' },
        { id: 't2', subject: 'Add the idempotency key to the order endpoint', status: 'completed' },
        { id: 't3', subject: 'Write the migration and back-fill', status: 'in_progress' },
        { id: 't4', subject: 'Cover the retry path with tests', status: 'pending' },
        { id: 't5', subject: 'Update the API reference', status: 'pending' },
      ],
      files: [
        { path: 'C:/code/storefront/src/checkout/order.ts', rel: 'src/checkout/order.ts', name: 'order.ts', edits: 4 },
        { path: 'C:/code/storefront/db/migrations/0042_idempotency.sql', rel: 'db/migrations/0042_idempotency.sql', name: '0042_idempotency.sql', edits: 1, created: true },
        { path: 'C:/code/storefront/src/checkout/retry.ts', rel: 'src/checkout/retry.ts', name: 'retry.ts', edits: 2 },
        { path: 'C:/code/storefront/docs/api.md', rel: 'docs/api.md', name: 'api.md', edits: 1 },
      ],
      messages: [
        { id: 'm1', role: 'user', when: when(9), blocks: [
          { kind: 'text', text: 'Double-charging on retries. Make the order endpoint idempotent — key on the client request id, and back-fill the existing rows.' },
        ]},
        { id: 'm2', role: 'assistant', when: when(8), model: 'claude-opus-5', blocks: [
          { kind: 'text', text: 'The endpoint writes the order and *then* charges, so a retry that lands after the write but before the charge creates a second order. I will key on the request id and make the write conditional.' },
          { kind: 'tool', tool: { id: 'x1', name: 'Edit', pending: false,
            summary: 'src/checkout/order.ts',
            input: JSON.stringify({
              file_path: 'src/checkout/order.ts',
              old_string: 'export async function createOrder(req: OrderRequest) {\n  const order = await db.orders.insert(req)\n  await payments.charge(order)\n  return order\n}',
              new_string: 'export async function createOrder(req: OrderRequest) {\n  const existing = await db.orders.byIdempotencyKey(req.requestId)\n  if (existing) return existing\n  const order = await db.orders.insert({ ...req, idempotencyKey: req.requestId })\n  await payments.charge(order)\n  return order\n}',
            }),
            result: 'The file has been updated successfully.' } },
          { kind: 'text', text: 'The migration adds the column, a unique index on it, and back-fills the key from the existing request log so old rows are covered too.' },
          { kind: 'tool', tool: { id: 'x2', name: 'Bash', pending: false,
            summary: 'Run the checkout tests',
            input: JSON.stringify({ command: 'npm test -- checkout', description: 'Run the checkout tests' }),
            result: 'PASS  src/checkout/order.test.ts (18 tests)\nPASS  src/checkout/retry.test.ts (11 tests)\n\nTests: 29 passed, 29 total' } },
        ]},
      ],
    };
    CHAT.last = d;
    RAIL.sig = '';
    renderConversation(d, 's1');
    await new Promise(r => setTimeout(r, 400));
    // Open, because a folded card shows a filename and the whole argument for
    // rendering a tool call as the thing it is happens inside.
    for (const h of document.querySelectorAll('#chatBody .tool-head')) h.click();
    await new Promise(r => setTimeout(r, 400));
    resolve('drawn');
  } catch (e) { resolve('threw: ' + (e.stack || e.message)); }
})`); out != "drawn" {
		t.Fatalf("could not draw the conversation: %s", out)
	}
	view(850)
	c.evalString(t, `new Promise(r => setTimeout(() => r('ok'), 250))`)
	shot(t, c, dir, "conversation.png")

	// Ctrl-K, with the agents sorted by who wants something.
	view(620)
	c.evalString(t, `new Promise(async r => { openPalette(); await new Promise(x => setTimeout(x, 400)); r('ok'); })`)
	shot(t, c, dir, "palette.png")
	c.evalString(t, `new Promise(r => { closePalette(); r('ok'); })`)

	// Searching every transcript on the disk.
	c.evalString(t, `
new Promise(async r => {
  const now = Date.now();
  window.tryApi = async () => ({
    files: 1127, total: 1127, bytes: 2410 * 1048576, millis: 2714, truncated: false,
    hits: [
      { sessionId: 'aaaa1111', when: new Date(now - 40 * 60000).toISOString(), role: 'assistant',
        account: 'work', liveSession: 's1', agentName: 'Checkout', projectName: 'storefront',
        snippet: '…the double charge comes from the retry landing between the insert and the charge — an idempotency key on the request id closes it…',
        text: 'the double charge comes from the retry landing between the insert and the charge.' },
      { sessionId: 'bbbb2222', when: new Date(now - 3 * 86400000).toISOString(), role: 'user',
        account: 'work', projectName: 'billing-api',
        snippet: 'why are we seeing duplicate charges on the same request id?' },
      { sessionId: 'cccc3333', when: new Date(now - 6 * 86400000).toISOString(), role: 'assistant',
        account: 'personal', projectName: 'data-pipeline', sub: 'agent-a19',
        snippet: '…Bash — grep -rn "idempotency" services/ledger…' },
      { sessionId: 'dddd4444', when: new Date(now - 11 * 86400000).toISOString(), role: 'assistant',
        account: 'work', projectName: 'storefront',
        snippet: '…added a unique index on (customer_id, idempotency_key) so the database refuses the second write rather than trusting the application…' },
    ],
  });
  openFind('idempotency');
  await new Promise(x => setTimeout(x, 600));
  r('ok');
})`)
	view(560)
	c.evalString(t, `new Promise(r => setTimeout(() => r('ok'), 250))`)
	shot(t, c, dir, "search.png")
	c.evalString(t, `new Promise(r => { closeFind(); r('ok'); })`)

	// What happened while nobody was watching.
	c.evalString(t, `
new Promise(async r => {
  const now = new Date();
  const at = (o, h, m) => new Date(now.getFullYear(), now.getMonth(), now.getDate() - o, h, m).toISOString();
  const row = (o, h, m, who, proj, text, level, sid) =>
    ({ at: at(o, h, m), sessionId: sid || '', agentName: who, projectName: proj, text, level });
  const rows = [
    row(0, 9, 41, 'Storefront',  'storefront',    'Asked: Do you want to create src/checkout/Summary.tsx?', 'warn', 's2'),
    row(0, 9, 22, 'Checkout',    'storefront',    'Finished a turn', 'info', 's1'),
    row(0, 9, 4,  'Migrations',  'storefront',    'Finished a turn', 'info', 's3'),
    row(0, 8, 47, 'Load tests',  'storefront',    'Started on work', 'info', 's6'),
    row(0, 8, 12, 'Reviewer',    'storefront',    'Answered, and carried on', 'info', 's4'),
    row(1, 23, 58,'Migrations',  'storefront',    'personal took over from work', 'warn'),
    row(1, 23, 57,'Migrations',  'storefront',    'work is out of quota until 04:00', 'warn'),
    row(1, 22, 31,'Docs',        'data-pipeline', 'Finished a turn', 'info'),
    row(1, 2, 14, 'Night shift', 'billing-api',   'Ended with an error: the pty closed', 'bad'),
  ];
  window.tryApi = async p => p.startsWith('/activity') ? rows : [];
  leaveAgent();
  S.view = 'activity';
  ACT.scope = 'all';
  render();
  await new Promise(x => setTimeout(x, 600));
  r('ok');
})`)
	view(600)
	c.evalString(t, `new Promise(r => setTimeout(() => r('ok'), 300))`)
	shot(t, c, dir, "activity.png")
}

// shot saves one PNG of the whole viewport.
func shot(t *testing.T, c *chrome, dir, name string) {
	t.Helper()
	res := c.call(t, "Page.captureScreenshot", map[string]any{"format": "png"})
	data, _ := res["data"].(string)
	if data == "" {
		t.Fatalf("%s: Chrome returned no image", name)
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("%-18s %6.0f KB", name, float64(len(raw))/1024)
}
