import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { compileScript, parse } from '@vue/compiler-sfc';
import * as vue from 'vue';

const accountViewSource = await readFile(new URL('../src/components/AccountView.vue', import.meta.url), 'utf8');
const compiledAccountView = compileScript(parse(accountViewSource).descriptor, { id: 'account-auto-refresh-test', genDefaultAs: 'component' });
const accountViewImports = Object.keys(compiledAccountView.imports);
const buildAccountView = new Function(...accountViewImports, compiledAccountView.content.replace(/^import .+;?\r?$/gm, '') + '\nreturn component;');
const settle = async () => { for (let i = 0; i < 10; i++) { await vue.nextTick(); await new Promise(resolve => setImmediate(resolve)); } };

test('useAccounts persists 5-30s auto-refresh interval and supports quiet balance refresh', async () => {
  const mounted = [], unmounted = [], intervals = new Map(), calls = [], notices = [];
  const storage = new Map([['wb.accounts.provider', 'antigravity'], ['wb.accounts.auto-refresh-seconds', '15']]);
  let remaining = 0.65;
  const originals = { setInterval, clearInterval, document: globalThis.document };
  globalThis.document = { hidden: false };
  globalThis.setInterval = (cb, ms) => { intervals.set(ms, cb); return ms; };
  globalThis.clearInterval = id => intervals.delete(id);
  globalThis.autoRefreshHarness = {
    vue: { ...vue, onMounted: cb => mounted.push(cb), onBeforeUnmount: cb => unmounted.push(cb) },
    Message: Object.fromEntries(['success', 'warning', 'error'].map(kind => [kind, value => notices.push({ kind, value })])),
    hostRuntime: {
      apiBase: 'http://localhost/panel/api/',
      storage: { getItem: key => storage.get(key) ?? null, setItem: (key, value) => storage.set(key, value) },
      async fetchAPI(url) {
        const route = url.pathname.replace('/panel/api/', '');
        calls.push(route);
        if (route === 'antigravity/balance_all') remaining = 0.8;
        const body = route === 'overview'
          ? { antigravity_accounts: [{ uid: 'ag-1', provider: 'antigravity', quota_groups: [{ name: 'Gemini Models', buckets: [{ window: '5h', remaining }] }] }] }
          : route === 'auth/bootstrap' ? { api_key: 'test-key', key_available: true }
          : route === 'settings/checkin' ? { settings: {} } : {};
        return new Response(JSON.stringify(body));
      },
    },
  };
  const source = (await readFile(new URL('../src/useAccounts.js', import.meta.url), 'utf8'))
    .replace(/import \{([^}]+)\} from 'vue';/, 'const {$1} = globalThis.autoRefreshHarness.vue;')
    .replace("import { Message } from '@arco-design/web-vue';", 'const { Message } = globalThis.autoRefreshHarness;')
    .replace("import { hostRuntime } from './hostRuntime';", 'const { hostRuntime } = globalThis.autoRefreshHarness;')
    .replace("from './dateTime'", `from '${new URL('../src/dateTime.js', import.meta.url).href}'`);
  const scope = vue.effectScope();
  try {
    const mod = await import('data:text/javascript;base64,' + Buffer.from(source).toString('base64'));
    assert.deepEqual(mod.AUTO_REFRESH_OPTIONS, [0, 5, 10, 15, 20, 25, 30]);
    assert.equal(mod.normalizeAutoRefreshSeconds('10'), 10);
    assert.equal(mod.normalizeAutoRefreshSeconds('99'), 0);

    const model = scope.run(() => mod.useAccounts());
    assert.equal(model.autoRefreshSeconds.value, 15);
    model.setAutoRefreshSeconds(5);
    assert.equal(model.autoRefreshSeconds.value, 5);
    assert.equal(storage.get('wb.accounts.auto-refresh-seconds'), '5');
    model.setAutoRefreshSeconds(999);
    assert.equal(model.autoRefreshSeconds.value, 0);
    assert.equal(storage.get('wb.accounts.auto-refresh-seconds'), '0');

    mounted.forEach(cb => cb());
    await settle();
    await model.refreshBalances({ quiet: true });
    assert.equal(calls.filter(route => route === 'antigravity/balance_all').length, 1);
    assert.equal(model.antigravityAccounts.value[0].quota_groups[0].buckets[0].remaining, 0.8);
    assert.equal(notices.length, 0, 'quiet refresh must not emit toast notifications');
  } finally {
    unmounted.forEach(cb => cb());
    scope.stop();
    globalThis.setInterval = originals.setInterval;
    globalThis.clearInterval = originals.clearInterval;
    globalThis.document = originals.document;
    delete globalThis.autoRefreshHarness;
  }
});

test('AccountView schedules 5-30s auto-refresh, pauses when hidden, and triggers immediately when enabled from off', async t => {
  const mounted = [], unmounted = [], timers = new Map(), refreshCalls = [];
  let nextTimerId = 1, now = 1_700_000_000_000;
  const docEvents = new EventTarget();
  const originals = { document: globalThis.document, setTimeout: globalThis.setTimeout, clearTimeout: globalThis.clearTimeout, now: Date.now };
  globalThis.document = Object.assign(docEvents, { hidden: false });
  globalThis.setTimeout = (cb, ms) => {
    const id = nextTimerId++;
    timers.set(id, { cb, ms });
    return id;
  };
  globalThis.clearTimeout = id => timers.delete(id);
  Date.now = () => now;

  const model = vue.reactive({
    provider: 'antigravity',
    providerName: 'Antigravity',
    connected: true,
    loading: false,
    accounts: [{ uid: 'ag-1', provider: 'antigravity', quota_groups: [] }],
    pending: [],
    signed: [],
    eligible: [],
    remarks: {},
    expiryEntries: [],
    effectiveHours: [],
    now,
    busy: vue.reactive(new Set()),
    autoRefreshSeconds: 5,
    setAutoRefreshSeconds(v) { model.autoRefreshSeconds = v; },
    async refreshBalances(opts) { refreshCalls.push(opts || {}); },
    accountName: a => a.uid,
  });

  const lifecycle = { onMounted: cb => mounted.push(cb), onBeforeUnmount: cb => unmounted.push(cb) };
  const component = buildAccountView(...accountViewImports.map(name => {
    const entry = compiledAccountView.imports[name];
    if (entry.source === 'vue') return lifecycle[entry.imported] || vue[entry.imported];
    if (entry.source === '../useAccounts') {
      return {
        AUTO_REFRESH_OPTIONS: [0, 5, 10, 15, 20, 25, 30],
        providerSupportsCheckin: () => false,
        canCheckin: () => false,
        checkinState: () => ({ text: '', kind: 'muted' }),
        accountEdition: () => 'Antigravity',
        accountBadge: () => '',
        accountCredits: () => '0',
        formatHour: h => `${h}:00`,
        formatNumber: n => String(n),
      }[entry.imported];
    }
    return {};
  }));

  const scope = vue.effectScope();
  const view = scope.run(() => component.setup({ model }, { emit() {}, expose() {} }));
  t.after(() => {
    unmounted.splice(0).forEach(cb => cb());
    scope.stop();
    Object.assign(globalThis, { document: originals.document, setTimeout: originals.setTimeout, clearTimeout: originals.clearTimeout });
    Date.now = originals.now;
  });

  mounted.forEach(cb => cb());
  assert.equal(timers.size, 1);
  assert.equal([...timers.values()][0].ms, 5000);

  // Trigger first 5s auto-refresh.
  const first = [...timers.entries()][0];
  timers.delete(first[0]);
  await first[1].cb();
  assert.deepEqual(refreshCalls, [{ quiet: true }]);
  assert.equal(timers.size, 1);
  assert.equal([...timers.values()][0].ms, 5000);

  // Change interval to 30s.
  model.setAutoRefreshSeconds(30);
  await settle();
  assert.equal(timers.size, 1);
  assert.equal([...timers.values()][0].ms, 30000);

  // Hide document -> timer cleared.
  document.hidden = true;
  docEvents.dispatchEvent(new Event('visibilitychange'));
  assert.equal(timers.size, 0);

  // Return to document after 35s -> immediate catch-up timer (0ms).
  now += 35000;
  document.hidden = false;
  docEvents.dispatchEvent(new Event('visibilitychange'));
  assert.equal(timers.size, 1);
  assert.equal([...timers.values()][0].ms, 0);

  // Disable auto-refresh -> no active timer.
  model.setAutoRefreshSeconds(0);
  await settle();
  assert.equal(timers.size, 0);

  // Re-enabling from off (0 -> 10s) triggers an immediate refresh (0ms), then schedules 10s.
  model.setAutoRefreshSeconds(10);
  await settle();
  assert.equal(timers.size, 1);
  assert.equal([...timers.values()][0].ms, 0);
  const immediate = [...timers.entries()][0];
  timers.delete(immediate[0]);
  await immediate[1].cb();
  assert.equal(refreshCalls.length, 2);
  assert.deepEqual(refreshCalls[1], { quiet: true });
  assert.equal(timers.size, 1);
  assert.equal([...timers.values()][0].ms, 10000);
});
