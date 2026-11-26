import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import * as vue from 'vue';

test('Qoder status refresh exposes check-in only for accounts with an active reward campaign', async () => {
  const mounted = [], unmounted = [], intervals = new Map(), calls = [];
  const storage = new Map([['wb.accounts.provider', 'qoder']]);
  const original = {
    localStorage: globalThis.localStorage,
    document: globalThis.document,
    setInterval: globalThis.setInterval,
    clearInterval: globalThis.clearInterval,
  };
  globalThis.localStorage = {
    getItem: key => storage.get(key) || null,
    setItem: (key, value) => storage.set(key, value),
  };
  globalThis.document = { hidden: false, baseURI: 'http://localhost/panel/' };
  globalThis.setInterval = (callback, ms) => { intervals.set(ms, callback); return ms; };
  globalThis.clearInterval = id => intervals.delete(id);

  let accounts = [
    { uid: 'qoder-paid', provider: 'qoder', user_type: 'personal_standard', checkin_supported: false, checkin_done: false },
    { uid: 'qoder-eligible', provider: 'qoder', user_type: 'personal_standard', checkin_supported: false, checkin_done: false },
  ];
  globalThis.qoderHarness = {
    vue: { ...vue, onMounted: callback => mounted.push(callback), onBeforeUnmount: callback => unmounted.push(callback) },
    Message: Object.fromEntries(['success', 'warning', 'error'].map(kind => [kind, () => {}])),
    hostRuntime: {
      apiBase: 'http://localhost/panel/api/',
      storage: {
        getItem: key => storage.get(key) || null,
        setItem: (key, value) => storage.set(key, value),
      },
      async fetchAPI(url) {
        const route = url.pathname.replace('/panel/api/', '');
        calls.push(route);
        if (route === 'qoder/balance_all') {
          accounts = accounts.map(account => account.uid === 'qoder-eligible'
            ? { ...account, checkin_supported: true }
            : account);
        }
        const body = route === 'overview' ? { qoder_accounts: accounts.map(account => ({ ...account })) }
          : route === 'auth/bootstrap' ? { api_key: 'test-key', key_available: true }
            : route === 'settings/checkin' ? { settings: { checkin_enabled: true, checkin_hours: [9, 21] } }
              : route === 'logs' ? { entries: [] }
                : route === 'desktop/status' ? { clients: [] }
                  : {};
        return new Response(JSON.stringify(body));
      },
    },
  };

  const source = (await readFile(new URL('../src/useAccounts.js', import.meta.url), 'utf8'))
    .replace(/import \{([^}]+)\} from 'vue';/, 'const {$1} = globalThis.qoderHarness.vue;')
    .replace("import { Message } from '@arco-design/web-vue';", 'const { Message } = globalThis.qoderHarness;')
    .replace("import { hostRuntime } from './hostRuntime';", 'const { hostRuntime } = globalThis.qoderHarness;')
    .replace("from './dateTime'", `from '${new URL('../src/dateTime.js', import.meta.url).href}'`);
  const scope = vue.effectScope();
  const settle = async () => { for (let i = 0; i < 12; i++) { await vue.nextTick(); await new Promise(resolve => setImmediate(resolve)); } };

  try {
    const module = await import('data:text/javascript;base64,' + Buffer.from(source).toString('base64'));
    const model = scope.run(() => module.useAccounts());
    mounted.forEach(callback => callback());
    await settle();

    assert.equal(calls.filter(route => route === 'qoder/balance_all').length, 1);
    assert.equal(module.providerSupportsCheckin('qoder'), true);
    assert.equal(model.pending.value.length, 1);
    assert.equal(model.pending.value[0].uid, 'qoder-eligible');
    assert.equal(module.canCheckin(model.accounts.value[0]), false);
    assert.equal(module.canCheckin(model.accounts.value[1]), true);
    assert.equal(module.checkinState(model.accounts.value[1]).text, '待签到');
  } finally {
    unmounted.forEach(callback => callback());
    scope.stop();
    globalThis.localStorage = original.localStorage;
    globalThis.document = original.document;
    globalThis.setInterval = original.setInterval;
    globalThis.clearInterval = original.clearInterval;
    delete globalThis.qoderHarness;
  }
});
