// Linux acceptance of the real built SPA against local, non-production fixtures.
// Setup outside this repository (no dependency/lockfile changes):
//   npm install --prefix "$PW_DIR" --no-package-lock --ignore-scripts playwright@1.63.0
//   node "$PW_DIR/node_modules/playwright/cli.js" install --with-deps chromium
//   node web-react/scripts/reinstall-browser.mjs "$PW_DIR/node_modules/playwright"
import assert from 'node:assert/strict';
import { createReadStream } from 'node:fs';
import { readFile, stat } from 'node:fs/promises';
import { createServer } from 'node:http';
import { extname, join, resolve, sep } from 'node:path';
import { pathToFileURL } from 'node:url';

const PLAYWRIGHT_VERSION = '1.63.0'; // Verified published through npm view.
// Preview serves disposable API fixtures and the real built SPA. It does not
// launch or automate a browser; the desktop browser tools own that interaction.
const previewOnly = process.argv[2] === '--preview';
const previewPort = previewOnly ? Number(process.env.PSP_FIXTURE_PREVIEW_PORT ?? 0) : 0;
assert(Number.isInteger(previewPort) && previewPort >= 0 && previewPort <= 65535, 'Invalid preview-only loopback port.');
const packageDirectory = process.argv[2] && resolve(process.argv[2]);
let chromium, expect;
if (!previewOnly) {
  assert(packageDirectory && process.argv.length === 3,
    'usage: node web-react/scripts/reinstall-browser.mjs /ABS/PATH/node_modules/playwright | --preview');
  assert.equal(process.platform, 'linux', 'This acceptance gate requires a genuine Linux browser run.');
  const installedPackage = JSON.parse(await readFile(join(packageDirectory, 'package.json'), 'utf8'));
  assert.equal(installedPackage.name, 'playwright');
  assert.equal(installedPackage.version, PLAYWRIGHT_VERSION, 'Use the verified exact test-only Playwright version.');
  ({ chromium } = await import(pathToFileURL(join(packageDirectory, 'index.mjs')).href));
  ({ expect } = await import(pathToFileURL(join(packageDirectory, 'test.mjs')).href));
}

const dist = resolve(import.meta.dirname, '../../internal/web/dist');
const indexHTML = await readFile(join(dist, 'index.html'), 'utf8');
assert(indexHTML.includes('<!-- PSP_PANEL_BASE -->') && indexHTML.includes('type="module"'),
  'Build the production SPA before browser acceptance.');
const namespaces = Object.fromEntries(await Promise.all(['admin', 'common'].map(async namespace =>
  [namespace, JSON.parse(await readFile(join(import.meta.dirname, `../src/locales/en-US/${namespace}.json`), 'utf8'))])));
function text(key, variables = {}) {
  const [namespace, nested] = key.split(':');
  let value = nested.split('.').reduce((current, part) => current?.[part], namespaces[namespace]);
  assert.equal(typeof value, 'string', `Missing English fixture label ${key}`);
  for (const [name, replacement] of Object.entries(variables)) value = value.replaceAll(`{{${name}}}`, String(replacement));
  return value;
}
const s = key => text(`admin:servers.${key}`);
const closeName = text('common:actions.close');
const labels = { psp: 'Passwall Node', '3xui': '3X-UI', sui: 'S-UI' };
const servers = [
  { id: 7, panel_type: 'psp', name: 'Fixture Passwall Node', url: 'psp://fixture-agent-7',
    capabilities: ['core.upgrade'], auth_method: '', has_api_token: false, update_channel: 'stable',
    node_protocol_version: 1, node_effective_protocol_version: 1,
    node_capabilities: ['task.execution.v1', 'task.expiry.v1', 'task.agent.upgrade.v1'],
    node_compatibility: 'compatible', node_upgrade_ready: true },
  { id: 17, panel_type: '3xui', name: 'Fixture 3X-UI', url: 'https://fixture-3x.invalid',
    capabilities: ['panel.upgrade', 'core.upgrade'], auth_method: 'token', has_api_token: true },
  { id: 27, panel_type: 'sui', name: 'Fixture S-UI', url: 'https://fixture-sui.invalid',
    capabilities: [], auth_method: 'token', has_api_token: true },
].map(server => ({ has_password: false, insecure_https: false, core_version: '26.6.27',
  xray_version: '26.6.27', panel_version: server.panel_type === '3xui' ? '3.7.0' : server.panel_type === 'psp' ? '4.0.0 (fixture)' : '',
  compat_status: 'supported', ...server }));
const credential = 'fixture-fixed-node-credential-not-production';
// A server's page saves only what was changed, so the manual-configuration step types this note to have something to save.
const manualNote = 'fixture manual configuration note';
const version = '4.0.1';
const fingerprint = 'a'.repeat(64);
const requests = [];
const failures = [];
// Desktop preview additions are disposable and never broaden the release gate's
// allowed writes. Creating a fixture only creates an in-memory server record.
const previewCreatedIDs = new Set();
let origin;
const commandFor = id => previewOnly
  ? `bash -c 'set +a +x; umask 077; unset s; s=$(curl -qf --proto =https -m 30 --max-filesize 1048576 "$1") && [[ $s ]] && bash -n <<<"$s" 2>/dev/null && bash <<<"$s"' -- 'https://fixture-panel.invalid/node-bootstrap/${'a'.repeat(43)}'`
  : `sudo bash -c 'printf fixture-node-host-only-${id}'`;
const provisioning = id => ({ server: servers.find(server => server.id === id), agent_id: `fixture-agent-${id}`,
  credential, endpoint: previewOnly ? 'https://fixture-panel.invalid/v1/node/sync' : `${origin}/v1/node/sync` });
const command = id => ({ server_id: id, command: commandFor(id), expires_at: new Date(Date.now() + 900_000).toISOString() });
const count = (method, pathname) => requests.filter(request => request.method === method && request.pathname === pathname).length;
function reply(response, value) {
  response.writeHead(200, { 'Content-Type': 'application/json; charset=utf-8', 'Cache-Control': 'no-store' });
  response.end(JSON.stringify(value));
}
async function fixture(request, response, url) {
  let raw = '';
  for await (const chunk of request) {
    raw += chunk;
    assert(raw.length <= 65_536, 'Unexpected oversized API request');
  }
  const body = raw ? JSON.parse(raw) : undefined;
  const method = request.method;
  const pathname = url.pathname;
  requests.push({ method, pathname, body });
  if (pathname.startsWith('/api/admin/')) assert.equal(request.headers.authorization, 'Bearer fixture-admin-access');
  if (method === 'GET') {
    if (pathname === '/api/i18n/langs') return reply(response, []);
    if (pathname === '/api/auth/methods') return reply(response, { local: true, site_title: 'Isolated browser acceptance', app_title: 'Fixture PSP', timezone: 'UTC' });
    if (pathname === '/api/version') return reply(response, { version: 'fixture', commit: 'fixture', build_date: '' });
    if (pathname === '/api/admin/alerts') return reply(response, { alerts: [], counts: { error: 0, warning: 0, info: 0 } });
    if (pathname === '/api/admin/servers') return reply(response, { items: servers, total: servers.length, page: 1, page_size: 25 });
    // The servers page reads the access-control fleet status for its native rows.
    // A fleet with no policy: the status line stays hidden on every row.
    if (pathname === '/api/admin/dest/status') return reply(response, {
      generation: 0, published_generation: 0, last_write_at: null, next_publish_at: null, apply_eta_ms: 0,
      paused: false, publish_error: null, publish_error_at: null, geosite_release: '', geoip_unavailable: false,
      totals: {}, nodes: [], policy_notes: {},
    });
    // Match GetSUIRelease's read-only metadata DTO; no upstream request is made.
    // The servers page counts each server's nodes and lines from the node list; this fixture carries none.
    if (pathname === '/api/admin/nodes') return reply(response, { items: [], total: 0, page: 1, page_size: 200 });
    if (pathname === '/api/admin/servers/sui-release') return reply(response, { version: 'v1.6.2' });
    // THE SERVERS PAGE READS THE PANEL'S OWN COMPATIBILITY STATE, so the fixture has
    // to answer it: a path this does not know is a throw, and the acceptance counts a
    // fixture error as its own failure. The state it reports is the UNREMARKABLE one —
    // a range with no failed refresh and no policy — so the page renders exactly what
    // it rendered before it started asking, and the banner logic stays where it is
    // covered deliberately, in ServersView.updates.test.tsx.
    if (pathname === '/api/admin/servers/compat-status') return reply(response, {
      xui: { min_version: '3.4.2', max_tested: '3.8.5', refreshed_at: '2026-09-12T13:00:00Z' },
      sui: { max_tested: '1.6.3', refreshed_at: '2026-09-12T13:00:00Z' },
      policy: { installed: false, applicable: false, enforcing: false, expired: false },
    });
    if (pathname === '/api/admin/servers/node-releases') return reply(response, {
      checked_at: new Date().toISOString(), releases: [{ version, channel: 'beta',
        published_at: '2026-09-12T12:00:00Z', notes: 'Reviewed browser acceptance fixture',
        // THE ADDRESS IS THE TAG AND THE RECORD IS THE VERSION, which are never the same
        // string — and a version no longer determines the address, so the panel states it
        // and this stands in for what a real panel sends. The page URL is built from it,
        // and the surface rejects a release whose page is addressed another way.
        release_tag: `release/${version}`,
        release_url: `https://github.com/KazuhaHub/Passwall-Node/releases/tag/release/${version}`,
        methods: ['linux', 'docker', 'manual'], platforms: ['amd64', 'arm64'].map(arch => ({ os: 'linux', arch })) }],
    });
    // THE UPGRADE DIALOG ASKS THE INSTANCE WHICH TARGETS ARE REACHABLE, so the
    // fixture has to answer it: a route this does not know is a throw, and the
    // acceptance counts a fixture error as its own failure. The one target is the
    // release the catalog above publishes, offered along a verified edge — which is
    // what the dialog worked out for itself before it started asking, so the
    // surface behaves identically and the question is answered rather than dodged.
    if (pathname === '/api/admin/servers/7/upgrade-options') return reply(response, {
      component: 'agent', state: 'ready', current_version: '4.0.0', target_version: version,
      target_pinnable: true, reason_codes: [],
      targets: [{ version, offered_by_policy: true }],
    });
    if (pathname === '/api/admin/servers/7/node-installation') return reply(response, provisioning(7));
    if (pathname === '/api/admin/servers/7/node-agent-status') return reply(response, { state: 'running', core_state: 'running', configured_nodes: 3 });
    const createdRead = previewOnly && pathname.match(/^\/api\/admin\/servers\/(\d+)\/(node-installation|node-agent-status)$/);
    if (createdRead && previewCreatedIDs.has(Number(createdRead[1]))) {
      return reply(response, createdRead[2] === 'node-installation'
        ? provisioning(Number(createdRead[1])) : { state: 'waiting', configured_nodes: 0 });
    }
    if (pathname === '/api/admin/servers/17/node-migration-preview') return reply(response, {
      server_id: 17, server_name: servers[1].name, core_version: '26.6.27', recommended_core_version: '26.6.27',
      core_requires_ack: false, allow_restricted_reality: false, fingerprint, node_count: 3, client_count: 5,
      can_migrate: true, blockers: [], warnings: [{ code: 'managed_scope' }],
    });
  }
  if (method === 'POST') {
    if (previewOnly && pathname === '/api/admin/servers') {
      assert(previewCreatedIDs.size < 10, 'Preview server limit reached. Restart the disposable preview.');
      assert(body?.panel_type === 'psp' && typeof body.name === 'string' && body.name.trim() && body.name.length <= 64);
      assert(['stable', 'rc', 'beta', 'testing'].includes(body.update_channel));
      assert(Object.keys(body).every(key => ['name', 'panel_type', 'remark', 'update_channel'].includes(key)));
      assert(body.remark === undefined || typeof body.remark === 'string' && body.remark.length <= 1024);
      const id = 47 + previewCreatedIDs.size;
      servers.push({ id, panel_type: 'psp', name: body.name, remark: body.remark ?? '', url: `psp://fixture-agent-${id}`,
        capabilities: ['core.upgrade'], auth_method: '', has_api_token: false, has_password: false,
        insecure_https: false, update_channel: body.update_channel, panel_version: '', core_version: '', xray_version: '' });
      previewCreatedIDs.add(id);
      return reply(response, provisioning(id));
    }
    if (pathname === '/api/admin/servers/probe') {
      assert(servers.some(record => record.id === body?.id), 'Probe must address an original fixture server.');
      assert.deepEqual(body, { id: body.id }, 'The aggregate probe API accepts only its server ID.');
      return reply(response, previewCreatedIDs.has(body.id)
        ? { ok: false, inbound_count: 0, error: 'Isolated fixture: waiting for node heartbeat' }
        : { ok: true, inbound_count: 3 });
    }
    if (pathname === '/api/admin/servers/7/node-install-command') {
      // THE BODY IS THE CONTRACT, and it gained the mode: the panel states which of
      // the two installation decisions it is asking for rather than leaving the
      // installer to infer one from a version string.
      assert.deepEqual(body, { version, mode: 'install' });
      return reply(response, command(7));
    }
    const createdCommand = previewOnly && pathname.match(/^\/api\/admin\/servers\/(\d+)\/node-install-command$/);
    if (createdCommand && previewCreatedIDs.has(Number(createdCommand[1]))) {
      assert.deepEqual(body, { version, mode: 'install' });
      return reply(response, command(Number(createdCommand[1])));
    }
    if (pathname === '/api/admin/servers/17/node-migration-command') {
      assert.deepEqual(body, { version, fingerprint, core_version: '26.6.27',
        allow_restricted_reality: false, managed_only: true, confirm_single_instance: true });
      return reply(response, command(17));
    }
  }
  if (method === 'PUT' && /^\/api\/admin\/servers\/(17|27)$/.test(pathname)) {
    const id = Number(pathname.split('/').at(-1));
    const original = servers.find(record => record.id === id);
    assert.deepEqual(body, { panel_type: original.panel_type, url: original.url, name: original.name,
      username: '', remark: manualNote, auth_method: 'token', insecure_https: false });
    return reply(response, original);
  }
  if (method === 'PUT' && pathname === '/api/admin/servers/7') {
    assert.deepEqual(body, { name: servers[0].name, remark: '', update_channel: 'beta' });
    servers[0].update_channel = 'beta';
    return reply(response, servers[0]);
  }
  throw new Error(`Unexpected API/write: ${method} ${pathname}`);
}

const mime = { '.js': 'text/javascript', '.css': 'text/css', '.png': 'image/png', '.svg': 'image/svg+xml',
  '.woff': 'font/woff', '.woff2': 'font/woff2', '.ico': 'image/x-icon' };
const server = createServer(async (request, response) => {
  try {
    const url = new URL(request.url, 'http://127.0.0.1');
    if (url.pathname.startsWith('/api/')) return await fixture(request, response, url);
    if (url.pathname === '/admin/servers' || url.pathname.startsWith('/admin/servers/') || url.pathname === '/') {
      response.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' });
      const currentIndex = previewOnly ? await readFile(join(dist, 'index.html'), 'utf8') : indexHTML;
      let html = currentIndex.replace('<!-- PSP_PANEL_BASE -->', '<base href="/"><meta name="psp-panel-path" content="">');
      if (previewOnly) html = html.replace('</head>', `<script>
localStorage.setItem('psp-lang', 'zh-CN');
localStorage.setItem('psp_access', 'fixture-admin-access');
localStorage.setItem('psp_user', JSON.stringify({ userId: 1, upn: 'fixture-admin', displayName: 'Isolated preview', role: 'admin' }));
</script></head>`);
      response.end(html);
      return;
    }
    const candidate = resolve(dist, `.${decodeURIComponent(url.pathname)}`);
    assert(candidate.startsWith(`${dist}${sep}`) && (await stat(candidate)).isFile(), 'Unexpected static path');
    response.writeHead(200, { 'Content-Type': mime[extname(candidate)] || 'application/octet-stream' });
    createReadStream(candidate).pipe(response);
  } catch (error) {
    failures.push(error.message);
    response.writeHead(500, { 'Content-Type': 'application/json' });
    response.end(JSON.stringify({ error: 'Unexpected isolated fixture request' }));
  }
});

let browser;
try {
  await new Promise(resolveListen => server.listen(previewPort, '127.0.0.1', resolveListen));
  origin = `http://127.0.0.1:${server.address().port}`;
  if (previewOnly) {
    console.log(`Isolated built-SPA preview (no real installation): ${origin}/admin/servers`);
    await new Promise(done => { process.once('SIGINT', done); process.once('SIGTERM', done); });
  } else {
  browser = await chromium.launch({ headless: true, timeout: 30_000, args: ['--no-sandbox', '--disable-dev-shm-usage'] });
  const context = await browser.newContext({ viewport: { width: 1440, height: 1100 }, locale: 'en-US', serviceWorkers: 'block' });
  await context.grantPermissions(['clipboard-read', 'clipboard-write'], { origin });
  // Documented public auth persistence, not an imported store or private hook.
  await context.addInitScript(() => {
    localStorage.setItem('psp-lang', 'en-US');
    localStorage.setItem('psp_access', 'fixture-admin-access');
    localStorage.setItem('psp_user', JSON.stringify({ userId: 1, upn: 'fixture-admin', displayName: 'Fixture admin', role: 'admin' }));
  });
  await context.route('**/*', route => {
    const url = new URL(route.request().url());
    if (url.origin !== origin && !['data:', 'blob:'].includes(url.protocol)) {
      failures.push(`Unexpected external request: ${url.origin}`);
      return route.abort();
    }
    return route.continue();
  });
  const page = await context.newPage();
  page.setDefaultTimeout(10_000);
  page.on('pageerror', error => failures.push(`pageerror: ${error.message}`));
  page.on('console', message => { if (message.type() === 'error') failures.push(`console.error: ${message.text()}`); });
  page.on('response', response => { if (response.status() >= 400) failures.push(`HTTP ${response.status()}: ${new URL(response.url()).pathname}`); });

  // The row's More menu button is named for its server ("More actions for <name>"), and Edit lives in that menu.
  const rowOf = record => page.getByRole('row').filter({ has: page.getByText(record.name, { exact: true }) });
  const moreOf = record => rowOf(record).getByRole('button', { name: text('admin:servers.action.more_for', { name: record.name }), exact: true });
  // The backend, method, channel and version pickers are native selects: they are chosen by value, not opened.
  const backendOf = dialog => dialog.getByRole('combobox', { name: s('install_reinstall.backend'), exact: true });
  const optionsOf = select => select.locator('option');
  async function openChooser(record) {
    await moreOf(record).click();
    await expect(page.getByRole('menuitem', { name: s('install_reinstall.action'), exact: true })).toHaveCount(1);
    if (record.panel_type === 'psp') await expect(page.getByRole('menuitem', { name: s('agent_upgrade.action'), exact: true })).toBeVisible();
    await page.getByRole('menuitem', { name: s('install_reinstall.action'), exact: true }).click();
    const dialog = page.getByRole('dialog');
    await expect(backendOf(dialog)).toHaveValue(record.panel_type);
    await expect(backendOf(dialog).locator('option:checked')).toHaveText(labels[record.panel_type]);
    await expect(dialog.getByText(text('admin:servers.install_reinstall.original', { id: record.id, backend: labels[record.panel_type] }), { exact: true })).toBeVisible();
    return dialog;
  }
  // In a dialog the footer's Close comes before the corner X, so first() is the footer.
  // A dialog that has just been left is still animating out: wait until it is gone before the next opens.
  const gone = () => expect(page.getByRole('dialog')).toHaveCount(0);
  // A server's page and its installation page are pages of their own: the back link (the same on every object page)
  // returns to the list, and the list is back once its heading is.
  const toList = async () => {
    await page.locator('[data-slot="object-back"]').click();
    await expect(page.getByRole('heading', { name: s('title'), exact: true })).toBeVisible();
  };
  const close = dialog => dialog.getByRole('button', { name: closeName, exact: true }).first();
  async function selectReviewedRelease(dialog) {
    await expect(dialog.getByText(s('native.release_no_stable'), { exact: true })).toBeVisible();
    await expect(dialog.getByRole('combobox', { name: s('native.agent_version'), exact: true })).toBeDisabled();
    await dialog.getByRole('combobox', { name: s('native.release_channel'), exact: true }).selectOption('beta');
    await dialog.getByRole('combobox', { name: s('native.agent_version'), exact: true }).selectOption(version);
  }
  async function copyAndCheck(dialog, expected) {
    const field = dialog.getByLabel(s('native.install_command'), { exact: true });
    await expect(field).toHaveValue(expected);
    await expect(field).toHaveAttribute('readonly', '');
    await expect(field).toHaveJSProperty('tagName', 'INPUT');
    await dialog.getByRole('button', { name: s('native.copy_command'), exact: true }).click();
    await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe(expected);
    await expect(dialog.getByText(s('native.copied'), { exact: true })).toBeVisible();
  }
  async function inspectOriginalIdentity(dialog) {
    const advanced = dialog.getByRole('button', { name: s('native.advanced'), exact: true });
    await expect(advanced).toHaveAttribute('aria-expanded', 'false');
    await expect(dialog.getByLabel(s('native.credential'), { exact: true })).toHaveCount(0);
    await advanced.focus();
    await page.keyboard.press('Enter');
    await expect(advanced).toHaveAttribute('aria-expanded', 'true');
    await expect(dialog.getByLabel(s('native.agent_id'), { exact: true })).toHaveValue('fixture-agent-7');
    await expect(dialog.getByLabel(s('native.credential'), { exact: true })).toHaveValue(credential);
    await advanced.click();
    await expect(advanced).toHaveAttribute('aria-expanded', 'false');
  }
  await page.goto(`${origin}/admin/servers?lang=en-US`);
  await expect(page.getByRole('heading', { name: s('title'), exact: true })).toBeVisible();

  for (const record of servers.slice(1)) {
    const dialog = await openChooser(record);
    if (record.panel_type === 'sui') {
      await backendOf(dialog).selectOption('psp');
      await expect(dialog.getByText(s('install_reinstall.switch_unavailable'), { exact: true })).toBeVisible();
      await expect(dialog.getByRole('button', { name: s('install_reinstall.continue'), exact: true })).toBeDisabled();
      await backendOf(dialog).selectOption('sui');
    }
    await expect(dialog.getByText(s('install_reinstall.manual_unverified'), { exact: true })).toBeVisible();
    await expect(dialog.getByText(s('install_reinstall.manual_restore'), { exact: true })).toContainText('PSP does not perform this recovery for you');
    await expect(dialog.getByText(s('install_reinstall.manual_configure'), { exact: true })).toContainText('API connection alone does not mean');
    const guide = dialog.getByRole('link', { name: text('admin:servers.install_reinstall.official_guide', { backend: labels[record.panel_type] }), exact: true });
    await expect(guide).toHaveAttribute('href', record.panel_type === '3xui' ? 'https://github.com/MHSanaei/3x-ui' : 'https://github.com/alireza0/s-ui');
    // The automatic method is listed so the person sees it exists, and is off.
    await expect(optionsOf(dialog.getByRole('combobox', { name: s('install_reinstall.method'), exact: true }))
      .filter({ hasText: s('install_reinstall.automatic_unavailable') })).toBeDisabled();
    await dialog.getByRole('button', { name: s('install_reinstall.configure_original'), exact: true }).click();
    // The server's own page opens (its name is the page's heading) and the chooser has gone.
    await expect(page.getByRole('heading', { level: 1, name: record.name, exact: true })).toBeVisible();
    await gone();
    await expect(page.getByRole('combobox', { name: s('field.panel_type'), exact: true })).toBeDisabled();
    await expect(page.getByRole('combobox', { name: s('field.update_channel'), exact: true })).toHaveCount(0);
    await expect(page.getByLabel(s('field.url'), { exact: false })).toHaveValue(record.url);
    // Nothing is offered to save until something changes; the manual step records a note on the original ID.
    await expect(page.getByRole('button', { name: text('common:actions.save'), exact: true })).toHaveCount(0);
    await page.getByRole('textbox', { name: s('field.remark'), exact: true }).fill(manualNote);
    await page.getByRole('button', { name: text('common:actions.save'), exact: true }).click();
    await expect.poll(() => count('PUT', `/api/admin/servers/${record.id}`)).toBe(1);
    await expect(page.getByRole('button', { name: text('common:actions.save'), exact: true })).toHaveCount(0);
    await toList();
    assert.equal(count('PUT', `/api/admin/servers/${record.id}`), 1, 'Manual configuration must address the original ID.');
  }
  assert.equal(requests.some(request => /node-(migration|installation)|rotate-node/.test(request.pathname)), false,
    'Merely opening manual guidance must not issue migration/credential calls.');

  const pn = await openChooser(servers[0]);
  await expect(optionsOf(backendOf(pn))).toHaveText(['Passwall Node', '3X-UI', 'S-UI']);
  await backendOf(pn).selectOption('psp');
  await pn.getByRole('button', { name: s('install_reinstall.continue'), exact: true }).click();
  // The chooser hands over to the installation page: wait for the page's own controls, then no dialog is open.
  await expect(page.getByRole('combobox', { name: s('native.method_label'), exact: true })).toBeVisible();
  await gone();
  // The page is the scope the installation's helpers search.
  let dialog = page;
  await inspectOriginalIdentity(dialog);
  await expect(dialog.getByRole('button', { name: s('native.generate_command'), exact: true })).toBeDisabled();
  await selectReviewedRelease(dialog);
  await dialog.getByRole('button', { name: s('native.generate_command'), exact: true }).click();
  await copyAndCheck(dialog, commandFor(7));
  await toList();
  await moreOf(servers[0]).click();
  await page.getByRole('menuitem', { name: s('action.edit'), exact: true }).click();
  await expect(page.getByRole('heading', { level: 1, name: servers[0].name, exact: true })).toBeVisible();
  await expect(dialog.getByRole('combobox', { name: s('field.update_channel'), exact: true })).toHaveValue('stable');
  await dialog.getByRole('combobox', { name: s('field.update_channel'), exact: true }).selectOption('beta');
  await dialog.getByRole('button', { name: text('common:actions.save'), exact: true }).click();
  await expect.poll(() => count('PUT', '/api/admin/servers/7')).toBe(1);
  assert.equal(count('PUT', '/api/admin/servers/7'), 1, 'The saved update preference must address the original PN ID.');
  await toList();
  await (await openChooser(servers[0])).getByRole('button', { name: s('install_reinstall.continue'), exact: true }).click();
  await expect(page.getByRole('combobox', { name: s('native.method_label'), exact: true })).toBeVisible();
  await gone();
  await inspectOriginalIdentity(dialog);
  await expect(dialog.getByRole('combobox', { name: s('native.release_channel'), exact: true })).toHaveValue('beta');
  await expect(dialog.getByRole('combobox', { name: s('native.agent_version'), exact: true })).toHaveValue(version);
  await expect(dialog.getByRole('button', { name: s('native.generate_command'), exact: true })).toBeEnabled();
  // A temporary override must not save a second preference or replace identity.
  await dialog.getByRole('combobox', { name: s('native.release_channel'), exact: true }).selectOption('stable');
  assert.equal(count('PUT', '/api/admin/servers/7'), 1);
  await toList();
  await moreOf(servers[0]).click();
  await page.getByRole('menuitem', { name: s('agent_upgrade.action'), exact: true }).click();
  dialog = page.getByRole('dialog');
  await expect(dialog.getByRole('combobox', { name: s('native.release_channel'), exact: true })).toHaveValue('beta');
  // The upgrade surface selects the newest reviewed release in the saved
  // channel automatically; the administrator can still change it before
  // confirming the request.
  await expect(dialog.getByRole('button', { name: s('agent_upgrade.confirm'), exact: true })).toBeEnabled();
  await expect(dialog.getByRole('combobox', { name: s('native.agent_version'), exact: true })).toHaveValue(version);
  await close(dialog).click();
  await gone();

  const migration = await openChooser(servers[1]);
  await backendOf(migration).selectOption('psp');
  await expect(migration.getByText(text('admin:servers.install_reinstall.switch_warning', { original: '3X-UI', target: 'Passwall Node' }), { exact: true })).toBeVisible();
  assert.equal(count('GET', '/api/admin/servers/17/node-migration-preview'), 0);
  await migration.getByRole('button', { name: s('install_reinstall.precheck'), exact: true }).click();
  await expect(page.getByText(s('migration.ready'), { exact: true })).toBeVisible();
  await expect(page.getByRole('dialog')).toHaveCount(1);
  dialog = page.getByRole('dialog');
  await expect(dialog.getByText(s('migration.online_hint'), { exact: true })).toBeVisible();
  const migrationDetails = dialog.getByRole('button', { name: s('migration.details'), exact: true });
  await expect(migrationDetails).toHaveAttribute('aria-expanded', 'false');
  await expect(dialog.getByText(s('migration.node_command_hint'), { exact: true })).toHaveCount(0);
  await migrationDetails.click();
  await expect(dialog.getByText(s('migration.node_command_hint'), { exact: true })).toContainText('not on the PSP host');
  await migrationDetails.click();
  assert(!/migrate-server|docker compose stop|--all-psp-stopped/.test(await dialog.innerText()), 'Online default must not show offline PSP commands.');
  const generate = dialog.getByRole('button', { name: s('migration.generate_node_command'), exact: true });
  await expect(generate).toBeDisabled();
  await selectReviewedRelease(dialog);
  await expect(generate).toBeDisabled();
  await dialog.getByRole('checkbox', { name: s('migration.ack_managed_only'), exact: true }).check();
  await expect(generate).toBeDisabled();
  assert.equal(count('POST', '/api/admin/servers/17/node-migration-command'), 0);
  await dialog.getByRole('checkbox', { name: s('migration.single_instance_confirmation'), exact: true }).check();
  await expect(generate).toBeEnabled();
  await generate.click();
  await copyAndCheck(dialog, commandFor(17));
  await close(dialog).click();
  await gone();

  await page.getByRole('button', { name: s('create'), exact: true }).click();
  // Adding a server is a page of its own; it is the scope the following checks search.
  await expect(page.getByRole('heading', { level: 1, name: s('create'), exact: true })).toBeVisible();
  dialog = page;
  await expect(dialog.getByRole('combobox', { name: s('field.panel_type'), exact: true })).toHaveValue('psp');
  await expect(dialog.getByRole('combobox', { name: s('field.update_channel'), exact: true })).toHaveValue('stable');
  await expect(optionsOf(dialog.getByRole('combobox', { name: s('field.panel_type'), exact: true }))).toHaveText(['Passwall Node', '3X-UI', 'S-UI']);
  await dialog.getByRole('combobox', { name: s('field.panel_type'), exact: true }).selectOption('psp');
  // Re-selecting the type already chosen changes nothing, so the page is left without a question.
  await toList();
  await expect(page.getByRole('alertdialog')).toHaveCount(0);

  assert.equal(count('POST', '/api/admin/servers'), 0, 'Reinstallation must not create a server record.');
  assert(count('GET', '/api/admin/servers/sui-release') > 0, 'The visible S-UI server must request read-only release metadata.');
  assert.equal(requests.some(request => /rotate-node|node-credential$/.test(request.pathname)), false, 'Reinstallation must not rotate/import credentials.');
  assert.deepEqual(requests.filter(request => request.method === 'PUT').map(request => request.pathname),
    ['/api/admin/servers/17', '/api/admin/servers/27', '/api/admin/servers/7'], 'Only explicit original-ID configuration or preference saving may update a record.');
  assert.equal(requests.some(request => ['PATCH', 'DELETE'].includes(request.method)), false, 'No record deletion or arbitrary patch is allowed.');
  assert.equal(count('POST', '/api/admin/servers/7/node-install-command'), 1);
  assert.equal(count('POST', '/api/admin/servers/17/node-migration-command'), 1);
  // A result is kept for 60 s across the trip to a server's page and back, and dropped when that server is saved, so an ID
  // is probed again only after its own save: the first load, plus one more after each of 17, 27 and 7 was saved. Every
  // original server is probed and no other ID ever is. The ceiling is deliberately loose (a slow runner can outlive the
  // 60 s window and probe again on a return to the list); it is there to catch a probe loop, not to count round trips.
  const probed = requests.filter(request => request.method === 'POST' && request.pathname === '/api/admin/servers/probe').map(request => request.body.id);
  assert.deepEqual([...new Set(probed)].sort((a, b) => a - b), [7, 17, 27], 'Automatic probes must target only the original visible IDs.');
  for (const id of [7, 17, 27]) assert(probed.filter(probe => probe === id).length <= 8, `Server ${id} was probed in a loop.`);
  await context.close();
  assert.deepEqual(failures, [], 'Browser/fixture errors are acceptance failures.');
  console.log('PASS: real Linux Chromium built-SPA acceptance: original backends, PN defaults/full name, truthful manual recovery, fixed identity, reviewed-release consent and node-host command/copy.');
  }
} finally {
  await browser?.close();
  await new Promise(resolveClose => server.close(resolveClose));
}
