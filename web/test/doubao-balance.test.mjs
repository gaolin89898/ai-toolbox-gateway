import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import * as vue from 'vue';

test('Doubao refreshes on opening, deduplicates requests and updates paid totals', async () => {
  const mounted = [], unmounted = [], intervals = new Map(), calls = [], notices = [];
  const storage = new Map([['wb.accounts.provider', 'doubao']]);
  let now = Date.parse('2026-11-15T12:30:00Z'), fail = false;
  let account = { uid: 'doubao-test', provider: 'doubao', plan: '免费体验', credits: 3900, credits_total: 3900, balance_known: true };
  const paid = { ...account, plan: '标准套餐', credits: 4100, credits_total: 4100, available_credits: 6200, available_total: 6200 };
  const originals = { now: Date.now, setInterval, clearInterval, document: globalThis.document };
  Date.now = () => now;
  globalThis.document = { hidden: false };
  globalThis.setInterval = (callback, ms) => { intervals.set(ms, callback); return ms; };
  globalThis.clearInterval = ms => intervals.delete(ms);
  globalThis.doubaoHarness = {
    vue: { ...vue, onMounted: callback => mounted.push(callback), onBeforeUnmount: callback => unmounted.push(callback) },
    Message: Object.fromEntries(['success', 'warning', 'error'].map(kind => [kind, value => notices.push({ kind, value })])),
    hostRuntime: {
      apiBase: 'http://localhost/panel/api/',
      storage: { getItem: key => storage.get(key), setItem: (key, value) => storage.set(key, value) },
      async fetchAPI(url) {
        const route = url.pathname.replace('/panel/api/', '');
        calls.push(route);
        if (route === 'doubao/balance_all') {
          if (fail) return new Response(JSON.stringify({ error: 'quota service unavailable' }), { status: 502 });
          account = { ...paid };
        }
        const body = route === 'overview' ? { doubao_accounts: [{ ...account }] }
          : route === 'auth/bootstrap' ? { api_key: 'test-key', key_available: true }
          : route === 'settings/checkin' ? { settings: {} } : {};
        return new Response(JSON.stringify(body));
      },
    },
  };
  const source = (await readFile(new URL('../src/useAccounts.js', import.meta.url), 'utf8'))
    .replace(/import \{([^}]+)\} from 'vue';/, 'const {$1} = globalThis.doubaoHarness.vue;')
    .replace("import { Message } from '@arco-design/web-vue';", 'const { Message } = globalThis.doubaoHarness;')
    .replace("import { hostRuntime } from './hostRuntime';", 'const { hostRuntime } = globalThis.doubaoHarness;')
    .replace("from './dateTime'", `from '${new URL('../src/dateTime.js', import.meta.url).href}'`);
  const scope = vue.effectScope();
  const settle = async () => { for (let i = 0; i < 12; i++) { await vue.nextTick(); await new Promise(resolve => setImmediate(resolve)); } };
  try {
    const { useAccounts, accountCredits } = await import('data:text/javascript;base64,' + Buffer.from(source).toString('base64'));
    const model = scope.run(() => useAccounts());
    mounted.forEach(callback => callback());
    await settle();
    assert.equal(calls.filter(route => route === 'doubao/balance_all').length, 1);
    assert.equal(model.doubaoAccounts.value[0].plan, '标准套餐');
    assert.equal(model.credits.value, 6200);
    assert.equal(model.totalCredits.value, 6200);
    assert.equal(accountCredits(paid), '6,200');
    assert.equal(accountCredits({ ...paid, balance_known: false }), '--');
    assert.equal(notices.length, 0, 'automatic refresh must stay quiet');

    intervals.get(5000)(); intervals.get(5000)();
    model.selectProvider('trae'); model.selectProvider('doubao');
    await settle();
    assert.equal(calls.filter(route => route === 'doubao/balance_all').length, 1, 'rapid navigation and polling must not duplicate quota requests');

    now += 60001;
    paid.available_credits = 6175;
    intervals.get(5000)();
    await settle();
    assert.equal(model.credits.value, 6175, 'usage must update without a successful check-in');

    now += 60001; fail = true;
    intervals.get(5000)();
    await settle();
    assert.equal(model.credits.value, 6175, 'failed query must retain the last successful balance');
    assert.equal(notices.length, 0);
    globalThis.document.hidden = true;
    now += 60001;
    const before = calls.length;
    intervals.get(5000)();
    await settle();
    assert.equal(calls.length, before, 'hidden pages must not keep polling');
  } finally {
    unmounted.forEach(callback => callback()); scope.stop();
    Date.now = originals.now;
    globalThis.setInterval = originals.setInterval;
    globalThis.clearInterval = originals.clearInterval;
    globalThis.document = originals.document;
    delete globalThis.doubaoHarness;
  }
});
