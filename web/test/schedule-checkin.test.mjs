import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { compileScript, parse } from '@vue/compiler-sfc';
import * as vue from 'vue';

const scheduleViewSource = await readFile(new URL('../src/components/ScheduleView.vue', import.meta.url), 'utf8');
const compiledScheduleView = compileScript(parse(scheduleViewSource).descriptor, {
  id: 'schedule-view-test',
  genDefaultAs: 'component',
});
const scheduleViewImports = Object.keys(compiledScheduleView.imports);
const buildScheduleView = new Function(
  ...scheduleViewImports,
  compiledScheduleView.content.replace(/^import .+;?\r?$/gm, '') + '\nreturn component;',
);

const settle = async () => {
  for (let i = 0; i < 12; i++) {
    await vue.nextTick();
    await new Promise(resolve => setImmediate(resolve));
  }
};

async function loadUseAccounts(harnessKey, { provider = 'antigravity', overview = {}, schedule = { checkin_enabled: true, checkin_hours: [9, 21] } } = {}) {
  const mounted = [], unmounted = [], intervals = new Map(), calls = [], notices = [];
  const storage = new Map([['wb.accounts.provider', provider]]);
  const originals = {
    setInterval: globalThis.setInterval,
    clearInterval: globalThis.clearInterval,
    document: globalThis.document,
    localStorage: globalThis.localStorage,
  };
  globalThis.localStorage = {
    getItem: key => storage.get(key) ?? null,
    setItem: (key, value) => storage.set(key, value),
  };
  globalThis.document = { hidden: false, baseURI: 'http://localhost/panel/' };
  globalThis.setInterval = (cb, ms) => { intervals.set(ms, cb); return ms; };
  globalThis.clearInterval = id => intervals.delete(id);

  globalThis[harnessKey] = {
    vue: { ...vue, onMounted: cb => mounted.push(cb), onBeforeUnmount: cb => unmounted.push(cb) },
    Message: Object.fromEntries(['success', 'warning', 'error', 'info'].map(kind => [kind, value => notices.push({ kind, value })])),
    hostRuntime: {
      apiBase: 'http://localhost/panel/api/',
      storage: {
        getItem: key => storage.get(key) ?? null,
        setItem: (key, value) => storage.set(key, value),
      },
      async fetchAPI(url, options = {}) {
        const route = url.pathname.replace('/panel/api/', '');
        calls.push({ route, method: options.method || 'GET', body: options.body || '' });
        const body = route === 'overview' ? overview
          : route === 'auth/bootstrap' ? { api_key: 'test-key', key_available: true }
            : route === 'settings/checkin' ? { settings: schedule }
              : route === 'logs' ? { entries: [] }
                : route === 'desktop/status' ? { clients: [] }
                  : { ok: true };
        return new Response(JSON.stringify(body));
      },
    },
  };

  const source = (await readFile(new URL('../src/useAccounts.js', import.meta.url), 'utf8'))
    .replace(/import \{([^}]+)\} from 'vue';/, `const {$1} = globalThis.${harnessKey}.vue;`)
    .replace("import { Message } from '@arco-design/web-vue';", `const { Message } = globalThis.${harnessKey};`)
    .replace("import { hostRuntime } from './hostRuntime';", `const { hostRuntime } = globalThis.${harnessKey};`)
    .replace("from './dateTime'", `from '${new URL('../src/dateTime.js', import.meta.url).href}'`);
  const mod = await import('data:text/javascript;base64,' + Buffer.from(source).toString('base64'));
  const scope = vue.effectScope();
  const model = scope.run(() => mod.useAccounts());
  mounted.forEach(cb => cb());
  await settle();

  return {
    mod,
    model,
    calls,
    notices,
    cleanup() {
      unmounted.forEach(cb => cb());
      scope.stop();
      globalThis.setInterval = originals.setInterval;
      globalThis.clearInterval = originals.clearInterval;
      globalThis.document = originals.document;
      globalThis.localStorage = originals.localStorage;
      delete globalThis[harnessKey];
    },
  };
}

test('computeNextCheckin computes next same-day or next-day slot in Asia/Shanghai', async () => {
  const ctx = await loadUseAccounts('scheduleComputeHarness');
  try {
    const { computeNextCheckin } = ctx.mod;
    const morningNow = Date.parse('2026-11-25T08:30:00+08:00');
    const sameDay = computeNextCheckin([21, 9, 9, -1, 24], morningNow);
    assert.equal(sameDay.time, '09:00');
    assert.equal(sameDay.day, '今天');
    assert.equal(sameDay.remaining, '30 分钟');

    const nightNow = Date.parse('2026-11-25T22:15:00+08:00');
    const nextDay = computeNextCheckin([9, 21], nightNow);
    assert.equal(nextDay.time, '09:00');
    assert.equal(nextDay.day, '明天');
    assert.equal(computeNextCheckin([], nightNow), null);
  } finally {
    ctx.cleanup();
  }
});

test('checkinAllPlatforms triggers global and qoder endpoints based on allPending regardless of selected provider', async () => {
  const ctx1 = await loadUseAccounts('scheduleGlobalAllHarness', {
    provider: 'antigravity',
    overview: {
      accounts: [{ uid: 'wb-1', checkin_done: false, disabled: false, realm: 'cn' }],
      qoder_accounts: [{ uid: 'qd-1', provider: 'qoder', checkin_supported: true, checkin_done: false }],
      antigravity_accounts: [{ uid: 'ag-1', provider: 'antigravity' }],
    },
  });
  try {
    ctx1.calls.length = 0;
    await ctx1.model.checkinAllPlatforms();
    const posts = ctx1.calls.filter(c => c.method === 'POST').map(c => c.route);
    assert.deepEqual(posts.sort(), ['checkin_all', 'qoder/checkin_all']);
  } finally {
    ctx1.cleanup();
  }

  const ctx2 = await loadUseAccounts('scheduleMainOnlyHarness', {
    provider: 'doubao',
    overview: {
      trae_accounts: [{ uid: 'tr-1', provider: 'trae', checkin_done: false }],
      qoder_accounts: [{ uid: 'qd-1', provider: 'qoder', checkin_supported: false, checkin_done: false }],
    },
  });
  try {
    ctx2.calls.length = 0;
    await ctx2.model.checkinAllPlatforms();
    const posts = ctx2.calls.filter(c => c.method === 'POST').map(c => c.route);
    assert.deepEqual(posts, ['checkin_all']);
  } finally {
    ctx2.cleanup();
  }

  const ctx3 = await loadUseAccounts('scheduleQoderOnlyHarness', {
    provider: 'workbuddy',
    overview: {
      accounts: [{ uid: 'wb-1', checkin_done: true, disabled: false, realm: 'cn' }],
      qoder_accounts: [{ uid: 'qd-1', provider: 'qoder', checkin_supported: true, checkin_done: false }],
    },
  });
  try {
    ctx3.calls.length = 0;
    await ctx3.model.checkinAllPlatforms();
    const posts = ctx3.calls.filter(c => c.method === 'POST').map(c => c.route);
    assert.deepEqual(posts, ['qoder/checkin_all']);
  } finally {
    ctx3.cleanup();
  }
});

test('ScheduleView manages dirty state, 24h hour matrix, presets, platform coverage and 7-day history', async () => {
  const modCtx = await loadUseAccounts('scheduleViewUseAccountsHarness');
  try {
    const { canCheckin, chinaDate, clockTime, computeNextCheckin, formatHour, formatNumber } = modCtx.mod;
    const now = Date.parse('2026-11-25T10:30:00+08:00');
    const savedCalls = [];
    const notices = [];
    const navigated = [];
    const selectedProviders = [];

    const model = vue.reactive({
      now,
      connected: true,
      savedAt: null,
      busy: vue.reactive(new Set()),
      capabilities: { gateway: true },
      schedule: {
        checkin_enabled: true,
        checkin_hours: [21, 9],
        refresh_after_checkin: true,
        catchup_on_start: true,
      },
      allNextRun: computeNextCheckin([9, 21], now),
      qoderAccounts: [
        { uid: 'qd-1', provider: 'qoder', checkin_supported: true, checkin_done: false },
        { uid: 'qd-2', provider: 'qoder', checkin_supported: false, checkin_done: false },
      ],
      allAccounts: [
        { uid: 'wb-cn-1', realm: 'cn', checkin_done: true },
        { uid: 'wb-cn-2', realm: 'cn', checkin_done: false },
        { uid: 'wb-global', realm: 'global', checkin_done: false },
        { uid: 'wb-ent', realm: 'cn', enterprise: true, checkin_done: false },
        { uid: 'wb-off', realm: 'cn', disabled: true, checkin_done: false },
        { uid: 'tr-1', provider: 'trae', checkin_done: true },
        { uid: 'mm-1', provider: 'minimax', checkin_done: false },
        { uid: 'qd-1', provider: 'qoder', checkin_supported: true, checkin_done: false },
        { uid: 'qd-2', provider: 'qoder', checkin_supported: false, checkin_done: false },
        { uid: 'ag-1', provider: 'antigravity' },
      ],
      get allEligible() { return this.allAccounts.filter(canCheckin); },
      get allSigned() { return this.allEligible.filter(a => a.checkin_done); },
      get allPending() { return this.allEligible.filter(a => !a.checkin_done); },
      allLogs: [
        { ts: '2026-11-24T09:00:05+08:00', text: 'checkin wb-cn-1: 签到成功' },
        { ts: '2026-11-24T09:00:12+08:00', text: 'trae: checkin tr-1 签到成功' },
        { ts: '2026-11-23T10:00:04+08:00', text: 'minimax: checkin 完成' },
        { ts: '2026-11-23T10:00:10+08:00', text: 'qoder: checkin 完成' },
        { ts: '2026-11-25T09:00:02+08:00', text: 'checkin wb-cn-1: 今天已签到（幂等）' },
      ],
      async saveSchedule(next) {
        savedCalls.push(next);
        model.schedule = { ...next };
        model.savedAt = now;
      },
      selectProvider(id) { selectedProviders.push(id); },
      async checkinAllPlatforms() {},
    });

    const Stub = vue.defineComponent({ setup: (_, { slots }) => () => slots.default?.() });
    const useAccountsExports = { canCheckin, chinaDate, clockTime, computeNextCheckin, formatHour, formatNumber };
    const arcoExports = { Message: { info: msg => notices.push(msg) } };
    const component = buildScheduleView(...scheduleViewImports.map(name => {
      const entry = compiledScheduleView.imports[name];
      if (entry.source === 'vue') return vue[entry.imported];
      if (entry.source === '@arco-design/web-vue') return arcoExports[entry.imported] || Stub;
      if (entry.source === '@arco-design/web-vue/es/icon') return Stub;
      if (entry.source === '../useAccounts') return useAccountsExports[entry.imported];
      if (entry.source.startsWith('../assets/')) return 'icon.png';
      throw new Error(`Unexpected import in ScheduleView test: ${entry.source}`);
    }));

    const scope = vue.effectScope();
    const rawVm = scope.run(() => component.setup({ model, embedded: true }, { emit: (event, arg) => navigated.push([event, arg]), expose() {} }));
    const vm = vue.reactive(rawVm);
    await settle();

    try {
      assert.deepEqual(vm.draft.checkin_hours, [9, 21]);
      assert.equal(vm.isDirty, false);
      assert.equal(vm.eligibleCount, 5);
      assert.equal(vm.signedCount, 2);
      assert.equal(vm.pendingCount, 3);
      assert.equal(vm.activePlatformCount, 4);
      assert.equal(vm.skippedSummary.total, 5);
      assert.equal(vm.hasEarlyHourWithQoder, true);
      assert.equal(vm.recentRun.text, 'checkin wb-cn-1: 今天已签到（幂等）');

      const day23 = vm.history.find(item => item.date === '2026-11-23');
      const day24 = vm.history.find(item => item.date === '2026-11-24');
      const today = vm.history.find(item => item.today);
      assert.equal(day23.value, '2 次');
      assert.equal(day24.value, '2 次');
      assert.equal(today.value, '2 / 5');

      vm.toggleHour(14);
      assert.deepEqual(vm.draft.checkin_hours, [9, 14, 21]);
      assert.equal(vm.isDirty, true);
      assert.equal(vm.dirtyFields, 1);
      assert.equal(vm.previewNextRun.time, '14:00');

      vm.applyPreset({ hours: [8] });
      assert.deepEqual(vm.draft.checkin_hours, [8]);
      vm.toggleHour(8);
      assert.deepEqual(vm.draft.checkin_hours, [8]);
      assert.equal(notices.at(-1), '至少保留 1 个签到时刻');

      vm.reset();
      assert.equal(vm.isDirty, false);
      assert.deepEqual(vm.draft.checkin_hours, [9, 21]);

      vm.toggleHour(14);
      vm.draft.catchup_on_start = false;
      assert.equal(vm.dirtyFields, 2);
      vm.save();
      await settle();
      assert.equal(savedCalls.length, 1);
      assert.deepEqual(savedCalls[0], {
        checkin_enabled: true,
        checkin_hours: [9, 14, 21],
        refresh_after_checkin: true,
        catchup_on_start: false,
      });
      assert.equal(vm.isDirty, false);

      vm.openPlatformAccounts('trae');
      assert.deepEqual(selectedProviders, ['trae']);
      assert.deepEqual(navigated[0], ['navigate', 'accounts']);
    } finally {
      scope.stop();
    }
  } finally {
    modCtx.cleanup();
  }
});
