import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { compileScript, parse } from '@vue/compiler-sfc';
import * as vue from 'vue';

const source = await readFile(new URL('../src/components/GatewayView.vue', import.meta.url), 'utf8');
const compiled = compileScript(parse(source).descriptor, { id: 'gateway-refresh-test', genDefaultAs: 'component' });
const imports = Object.keys(compiled.imports);
const setup = new Function(...imports, compiled.content.replace(/^import .+;?\r?$/gm, '') + '\nreturn component;');
const settle = async () => { for (let i = 0; i < 8; i++) await vue.nextTick(); };

function harness(t) {
  const mounted = [], unmounted = [], intervals = new Map(), calls = [], notices = [];
  const events = { document: new EventTarget(), window: new EventTarget() };
  const original = Object.fromEntries(['document', 'window', 'location', 'setInterval', 'clearInterval'].map(key => [key, globalThis[key]]));
  globalThis.document = Object.assign(events.document, { hidden: false });
  globalThis.window = events.window;
  globalThis.location = { origin: 'http://localhost' };
  globalThis.setInterval = (callback, ms) => { intervals.set(ms, callback); return ms; };
  globalThis.clearInterval = id => intervals.delete(id);
  const model = { capabilities: { gateway: true }, overview: {}, api: async path => {
    calls.push(path);
    return state.api(path);
  } };
  const state = { revision: 1, fail: false, api: async path => {
    if (state.fail) throw new Error('offline');
    if (path === 'settings/gateway') return { config: {} };
    if (path.startsWith('usage?')) return {
      totals: { total_tokens: state.revision * 1000, credits: state.revision },
      by_model: [{ key: 'cn:test', total_tokens: state.revision * 1000 }],
      series: [{ scope: 'hour', t: new Date(Date.now() + 8 * 3600000).toISOString().slice(0, 13), prompt_tokens: state.revision * 1000 }],
    };
    if (path === 'request_metrics') return { completed: state.revision, failed: 0, archive: { enabled: true } };
    if (path.startsWith('request_logs?')) {
      const params = new URLSearchParams(path.split('?')[1]);
      return { entries: Array.from({ length: 45 }, (_, i) => ({
        request_id: `${state.revision}-${i}`, outcome: i === 0 ? 'http_error' : 'success', model: 'cn:test',
      })).filter(row => !params.get('outcome') || row.outcome === params.get('outcome')).slice(0, Number(params.get('limit'))) };
    }
    return {};
  } };
  const lifecycle = { onMounted: callback => mounted.push(callback), onBeforeUnmount: callback => unmounted.push(callback) };
  const component = setup(...imports.map(name => {
    const entry = compiled.imports[name];
    if (entry.source === 'vue') return lifecycle[entry.imported] || vue[entry.imported];
    if (entry.imported === 'Message') return Object.fromEntries(['error', 'warning', 'success', 'info'].map(kind => [kind, message => notices.push(message)]));
    if (name === 'formatNumber') return value => Number(value || 0).toLocaleString('zh-CN');
    if (name === 'clockTime') return String;
    return {};
  }));
  const scope = vue.effectScope();
  const view = scope.run(() => component.setup({ model }, { expose() {} }));
  const dispose = () => { unmounted.splice(0).forEach(callback => callback()); scope.stop(); };
  t.after(() => { dispose(); Object.assign(globalThis, original); });
  return { view, state, calls, notices, events, intervals, dispose, mount: async () => { mounted.forEach(callback => callback()); await settle(); } };
}

test('gateway refreshes usage totals, charts and model usage, and resumes on focus', async t => {
  const h = harness(t);
  await h.mount();
  assert.equal(h.view.chartKpis.value[0].value, '1K');
  h.state.revision = 2;
  await h.intervals.get(5000)();
  assert.equal(h.view.chartKpis.value[0].value, '2K');
  assert.equal(h.view.modelUsageRows.value[0].total_tokens, 2000);
  assert.equal(h.view.series.value.reduce((sum, point) => sum + point.prompt_tokens, 0), 2000);
  assert.equal(h.view.requestMetrics.value.completed, 2);
  assert.equal(h.view.feedRows.value[0].request_id, '2-0');

  document.hidden = true;
  const count = h.calls.length;
  await h.intervals.get(5000)();
  assert.equal(h.calls.length, count);
  h.state.revision = 3;
  document.hidden = false;
  h.events.document.dispatchEvent(new Event('visibilitychange'));
  await settle();
  assert.equal(h.view.chartKpis.value[0].value, '3K');
  h.state.revision = 4;
  h.events.window.dispatchEvent(new Event('focus'));
  await settle();
  assert.equal(h.view.chartKpis.value[0].value, '4K');

  h.state.fail = true;
  await h.intervals.get(5000)();
  assert.equal(h.view.chartKpis.value[0].value, '4K');
  assert.deepEqual(h.notices, []);
  h.state.fail = false;
  h.state.revision = 5;
  await h.intervals.get(5000)();
  assert.equal(h.view.chartKpis.value[0].value, '5K');
  h.dispose();
  const before = h.calls.length;
  h.events.window.dispatchEvent(new Event('focus'));
  h.events.document.dispatchEvent(new Event('visibilitychange'));
  await settle();
  assert.equal(h.intervals.size, 0);
  assert.equal(h.calls.length, before);
});

test('request logs refresh with filters and preserve pagination; returning to a cached tab refreshes', async t => {
  const h = harness(t);
  await h.mount();
  h.view.section.value = 'requests';
  await settle();
  assert.equal(h.view.requestSummary.value.find(item => item.label === '成功').value, '44');
  assert.equal(h.view.outcomeName({ outcome: 'success' }), '成功');
  assert.equal(h.view.requestSucceeded({ outcome: 'ok' }), true);
  h.view.reqPage.value = 2;
  h.state.revision = 2;
  await h.intervals.get(5000)();
  assert.equal(h.view.reqPage.value, 2);
  assert.equal(h.view.requestPageRows.value[0].request_id, '2-20');

  h.view.reqOutcome.value = 'http_error';
  h.view.reqRange.value = 86400;
  h.view.reqLimit.value = 300;
  await settle();
  await h.intervals.get(5000)();
  const query = new URLSearchParams(h.calls.findLast(path => path.startsWith('request_logs?')).split('?')[1]);
  assert.equal(query.get('outcome'), 'http_error');
  assert.equal(query.get('limit'), '300');
  assert.ok(Number(query.get('from')) > 0);
  assert.equal(h.view.requestRows.value.length, 1);
  assert.equal(h.view.reqPage.value, 1);

  h.state.revision = 3;
  h.view.section.value = 'usage';
  await settle();
  assert.equal(h.view.chartKpis.value[0].value, '3K');
  h.state.revision = 4;
  h.view.section.value = 'requests';
  await settle();
  assert.equal(h.view.requestRows.value[0].request_id, '4-0');
  h.view.section.value = 'models';
  await settle();
  const before = h.calls.length;
  await h.intervals.get(5000)();
  assert.equal(h.calls.length, before);
});

test('slow polling is deduplicated and cannot overwrite a newer manual request or window', async t => {
  const h = harness(t);
  await h.mount();
  const api = h.state.api;
  let release;
  h.state.api = path => path.startsWith('usage?') ? new Promise(resolve => { release = resolve; }) : api(path);
  const slowUsage = h.intervals.get(5000)();
  const count = h.calls.length;
  await h.intervals.get(5000)();
  assert.equal(h.calls.length, count);
  h.state.api = api;
  h.state.revision = 9;
  h.view.switchWindow(72);
  await settle();
  release({ totals: { total_tokens: 1000 } });
  await slowUsage;
  assert.equal(h.view.chartKpis.value[0].value, '9K');
  assert.equal(h.view.usageWindow.value, 72);

  h.view.section.value = 'requests';
  await settle();
  h.state.api = path => path.startsWith('request_logs?') ? new Promise(resolve => { release = resolve; }) : api(path);
  const slowLogs = h.intervals.get(5000)();
  h.view.reqOutcome.value = 'http_error';
  h.state.api = api;
  h.state.revision = 10;
  await h.view.load('requests');
  release({ entries: [{ request_id: 'stale-success', outcome: 'success' }] });
  await slowLogs;
  assert.deepEqual(h.view.requestRows.value.map(row => row.request_id), ['10-0']);
  assert.equal(h.view.loading.value, false);
});
