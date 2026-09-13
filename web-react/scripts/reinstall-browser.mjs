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
const packageDirectory = process.argv[2] && resolve(process.argv[2]);
assert(packageDirectory && process.argv.length === 3,
  'usage: node web-react/scripts/reinstall-browser.mjs /ABS/PATH/node_modules/playwright');
assert.equal(process.platform, 'linux', 'This acceptance gate requires a genuine Linux browser run.');
const installedPackage = JSON.parse(await readFile(join(packageDirectory, 'package.json'), 'utf8'));
assert.equal(installedPackage.name, 'playwright');
assert.equal(installedPackage.version, PLAYWRIGHT_VERSION, 'Use the verified exact test-only Playwright version.');
const { chromium } = await import(pathToFileURL(join(packageDirectory, 'index.mjs')).href);
const { expect } = await import(pathToFileURL(join(packageDirectory, 'test.mjs')).href);

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
    capabilities: ['core.upgrade'], auth_method: '', has_api_token: false },
  { id: 17, panel_type: '3xui', name: 'Fixture 3X-UI', url: 'https://fixture-3x.invalid',
    capabilities: ['panel.upgrade', 'core.upgrade'], auth_method: 'token', has_api_token: true },
  { id: 27, panel_type: 'sui', name: 'Fixture S-UI', url: 'https://fixture-sui.invalid',
    capabilities: [], auth_method: 'token', has_api_token: true },
].map(server => ({ has_password: false, insecure_https: false, core_version: '26.6.27',
  xray_version: '26.6.27', panel_version: server.panel_type === '3xui' ? '3.7.0' : '',
  compat_status: 'supported', ...server }));
const credential = 'fixture-fixed-node-credential-not-production';
const version = 'v0.0.1-beta3';
const fingerprint = 'a'.repeat(64);
const requests = [];
const failures = [];
let origin;
const commandFor = id => `sudo bash -c 'printf fixture-node-host-only-${id}'`;
const provisioning = id => ({ server: servers.find(server => server.id === id), agent_id: `fixture-agent-${id}`,
  credential, endpoint: `${origin}/v1/node/sync` });
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
    if (pathname === '/api/admin/servers/node-releases') return reply(response, {
      checked_at: new Date().toISOString(), releases: [{ version, channel: 'testing',
        published_at: '2026-09-12T12:00:00Z', notes: 'Reviewed browser acceptance fixture',
        release_url: `https://github.com/KazuhaHub/Passwall-Node/releases/tag/${version}`,
        methods: ['linux', 'docker', 'manual'], platforms: ['amd64', 'arm64'].map(arch => ({ os: 'linux', arch })) }],
    });
    if (pathname === '/api/admin/servers/7/node-installation') return reply(response, provisioning(7));
    if (pathname === '/api/admin/servers/7/node-agent-status') return reply(response, { state: 'running', core_state: 'running', configured_nodes: 3 });
    if (pathname === '/api/admin/servers/17/node-migration-preview') return reply(response, {
      server_id: 17, server_name: servers[1].name, core_version: '26.6.27', recommended_core_version: '26.6.27',
      core_requires_ack: false, allow_restricted_reality: false, fingerprint, node_count: 3, client_count: 5,
      can_migrate: true, blockers: [], warnings: [{ code: 'managed_scope' }],
    });
  }
  if (method === 'POST') {
    if (/^\/api\/admin\/servers\/(7|17|27)\/probe$/.test(pathname)) return reply(response, { ok: true, inbound_count: 3 });
    if (pathname === '/api/admin/servers/7/node-install-command') {
      assert.deepEqual(body, { version });
      return reply(response, command(7));
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
      username: '', remark: '', auth_method: 'token', insecure_https: false });
    return reply(response, original);
  }
  throw new Error(`Unexpected API/write: ${method} ${pathname}`);
}

const mime = { '.js': 'text/javascript', '.css': 'text/css', '.png': 'image/png', '.svg': 'image/svg+xml',
  '.woff': 'font/woff', '.woff2': 'font/woff2', '.ico': 'image/x-icon' };
const server = createServer(async (request, response) => {
  try {
    const url = new URL(request.url, 'http://127.0.0.1');
    if (url.pathname.startsWith('/api/')) return await fixture(request, response, url);
    if (url.pathname === '/admin/servers' || url.pathname === '/') {
      response.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' });
      response.end(indexHTML.replace('<!-- PSP_PANEL_BASE -->', '<base href="/"><meta name="psp-panel-path" content="">'));
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
  await new Promise(resolveListen => server.listen(0, '127.0.0.1', resolveListen));
  origin = `http://127.0.0.1:${server.address().port}`;
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

  async function openChooser(record) {
    const row = page.getByRole('row').filter({ has: page.getByText(record.name, { exact: true }) });
    await row.getByRole('button', { name: s('action.more'), exact: true }).click();
    await expect(page.getByRole('menuitem', { name: s('install_reinstall.action'), exact: true })).toHaveCount(1);
    if (record.panel_type === 'psp') await expect(page.getByRole('menuitem', { name: s('agent_upgrade.action'), exact: true })).toBeVisible();
    await page.getByRole('menuitem', { name: s('install_reinstall.action'), exact: true }).click();
    const dialog = page.getByRole('dialog');
    await expect(dialog.getByRole('combobox', { name: s('install_reinstall.backend'), exact: true })).toHaveText(labels[record.panel_type]);
    await expect(dialog.getByText(text('admin:servers.install_reinstall.original', { id: record.id, backend: labels[record.panel_type] }), { exact: true })).toBeVisible();
    return dialog;
  }
  async function selectReviewedRelease(dialog) {
    await expect(dialog.getByText(s('native.release_no_stable'), { exact: true })).toBeVisible();
    await expect(dialog.getByRole('combobox', { name: s('native.agent_version'), exact: true })).toHaveAttribute('aria-disabled', 'true');
    await dialog.getByRole('combobox', { name: s('native.release_channel'), exact: true }).click();
    await page.getByRole('option', { name: s('native.release_testing'), exact: true }).click();
    await dialog.getByRole('combobox', { name: s('native.agent_version'), exact: true }).click();
    await page.getByRole('option', { name: version, exact: true }).click();
  }
  async function copyAndCheck(dialog, expected) {
    const field = dialog.getByLabel(s('native.install_command'), { exact: true });
    await expect(field).toHaveValue(expected);
    await expect(field).toHaveAttribute('readonly', '');
    await dialog.getByRole('button', { name: s('native.copy_command'), exact: true }).click();
    await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe(expected);
  }
  await page.goto(`${origin}/admin/servers?lang=en-US`);
  await expect(page.getByRole('heading', { name: s('title'), exact: true })).toBeVisible();

  for (const record of servers.slice(1)) {
    const dialog = await openChooser(record);
    if (record.panel_type === 'sui') {
      await dialog.getByRole('combobox', { name: s('install_reinstall.backend'), exact: true }).click();
      await page.getByRole('option', { name: 'Passwall Node', exact: true }).click();
      await expect(dialog.getByText(s('install_reinstall.switch_unavailable'), { exact: true })).toBeVisible();
      await expect(dialog.getByRole('button', { name: s('install_reinstall.continue'), exact: true })).toBeDisabled();
      await dialog.getByRole('combobox', { name: s('install_reinstall.backend'), exact: true }).click();
      await page.getByRole('option', { name: 'S-UI', exact: true }).click();
    }
    await expect(dialog.getByText(s('install_reinstall.manual_unverified'), { exact: true })).toBeVisible();
    await expect(dialog.getByText(s('install_reinstall.manual_restore'), { exact: true })).toContainText('PSP does not perform this recovery for you');
    await expect(dialog.getByText(s('install_reinstall.manual_configure'), { exact: true })).toContainText('API connection alone does not mean');
    const guide = dialog.getByRole('link', { name: text('admin:servers.install_reinstall.official_guide', { backend: labels[record.panel_type] }), exact: true });
    await expect(guide).toHaveAttribute('href', record.panel_type === '3xui' ? 'https://github.com/MHSanaei/3x-ui' : 'https://github.com/alireza0/s-ui');
    await dialog.getByRole('combobox', { name: s('install_reinstall.method'), exact: true }).click();
    await expect(page.getByRole('option', { name: s('install_reinstall.automatic_unavailable'), exact: true })).toHaveAttribute('aria-disabled', 'true');
    await page.keyboard.press('Escape');
    await dialog.getByRole('button', { name: s('install_reinstall.configure_original'), exact: true }).click();
    await expect(page.getByRole('heading', { name: text('admin:servers.edit_title', { name: record.name }), exact: true })).toBeVisible();
    await expect(page.getByRole('dialog').getByRole('combobox', { name: s('field.panel_type'), exact: true })).toHaveAttribute('aria-disabled', 'true');
    await expect(page.getByRole('dialog').getByLabel(s('field.url'), { exact: false })).toHaveValue(record.url);
    await page.getByRole('dialog').getByRole('button', { name: text('common:actions.ok'), exact: true }).click();
    await expect(page.getByRole('dialog')).toHaveCount(0);
    assert.equal(count('PUT', `/api/admin/servers/${record.id}`), 1, 'Manual configuration must address the original ID.');
  }
  assert.equal(requests.some(request => /node-(migration|installation)|rotate-node/.test(request.pathname)), false,
    'Merely opening manual guidance must not issue migration/credential calls.');

  const pn = await openChooser(servers[0]);
  await pn.getByRole('combobox', { name: s('install_reinstall.backend'), exact: true }).click();
  await expect(page.getByRole('option')).toHaveText(['Passwall Node', '3X-UI', 'S-UI']);
  await page.getByRole('option', { name: 'Passwall Node', exact: true }).click();
  await pn.getByRole('button', { name: s('install_reinstall.continue'), exact: true }).click();
  let dialog = page.getByRole('dialog');
  await expect(dialog.getByLabel(s('native.agent_id'), { exact: true })).toHaveValue('fixture-agent-7');
  await expect(dialog.getByLabel(s('native.credential'), { exact: true })).toHaveValue(credential);
  await expect(dialog.getByRole('button', { name: s('native.generate_command'), exact: true })).toBeDisabled();
  await selectReviewedRelease(dialog);
  await dialog.getByRole('button', { name: s('native.generate_command'), exact: true }).click();
  await copyAndCheck(dialog, commandFor(7));
  await dialog.getByRole('button', { name: closeName, exact: true }).click();
  await (await openChooser(servers[0])).getByRole('button', { name: s('install_reinstall.continue'), exact: true }).click();
  dialog = page.getByRole('dialog');
  await expect(dialog.getByLabel(s('native.credential'), { exact: true })).toHaveValue(credential);
  await expect(dialog.getByLabel(s('native.agent_id'), { exact: true })).toHaveValue('fixture-agent-7');
  await dialog.getByRole('button', { name: closeName, exact: true }).click();

  const migration = await openChooser(servers[1]);
  await migration.getByRole('combobox', { name: s('install_reinstall.backend'), exact: true }).click();
  await page.getByRole('option', { name: 'Passwall Node', exact: true }).click();
  await expect(migration.getByText(text('admin:servers.install_reinstall.switch_warning', { original: '3X-UI', target: 'Passwall Node' }), { exact: true })).toBeVisible();
  assert.equal(count('GET', '/api/admin/servers/17/node-migration-preview'), 0);
  await migration.getByRole('button', { name: s('install_reinstall.precheck'), exact: true }).click();
  dialog = page.getByRole('dialog');
  await expect(dialog.getByText(s('migration.ready'), { exact: true })).toBeVisible();
  await expect(dialog.getByText(s('migration.online_hint'), { exact: true })).toBeVisible();
  await expect(dialog.getByText(s('migration.node_command_hint'), { exact: true })).toContainText('not on the PSP host');
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
  await dialog.getByRole('button', { name: closeName, exact: true }).click();

  await page.getByRole('button', { name: s('create'), exact: true }).click();
  dialog = page.getByRole('dialog');
  await expect(dialog.getByRole('combobox', { name: s('field.panel_type'), exact: true })).toHaveText('Passwall Node');
  await dialog.getByRole('combobox', { name: s('field.panel_type'), exact: true }).click();
  await expect(page.getByRole('option')).toHaveText(['Passwall Node', '3X-UI', 'S-UI']);
  await page.getByRole('option', { name: 'Passwall Node', exact: true }).click();
  await dialog.getByRole('button', { name: text('common:actions.cancel'), exact: true }).click();

  assert.equal(count('POST', '/api/admin/servers'), 0, 'Reinstallation must not create a server record.');
  assert.equal(requests.some(request => /rotate-node|node-credential$/.test(request.pathname)), false, 'Reinstallation must not rotate/import credentials.');
  assert.deepEqual(requests.filter(request => request.method === 'PUT').map(request => request.pathname),
    ['/api/admin/servers/17', '/api/admin/servers/27'], 'Only explicit original-ID manual configuration may update a record.');
  assert.equal(requests.some(request => ['PATCH', 'DELETE'].includes(request.method)), false, 'No record deletion or arbitrary patch is allowed.');
  assert.equal(count('POST', '/api/admin/servers/7/node-install-command'), 1);
  assert.equal(count('POST', '/api/admin/servers/17/node-migration-command'), 1);
  await context.close();
  assert.deepEqual(failures, [], 'Browser/fixture errors are acceptance failures.');
  console.log('PASS: real Linux Chromium built-SPA acceptance: original backends, PN defaults/full name, truthful manual recovery, fixed identity, reviewed-release consent and node-host command/copy.');
} finally {
  await browser?.close();
  await new Promise(resolveClose => server.close(resolveClose));
}
