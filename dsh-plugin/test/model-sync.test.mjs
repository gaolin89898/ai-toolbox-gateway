import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { modelCatalogSyncer } from '../lib/model-sync.js';

async function backend(t, handler) {
  const server = http.createServer(handler);
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => { server.closeAllConnections(); server.close(); });
  return server.address().port;
}

function services() {
  const doc = { providers: {
    'workbuddy-gateway': { baseURL: 'http://127.0.0.1:7863/v1', apiKeyEnv: 'TEST_REF', models: [{ id: 'old' }] },
    'opencode-go': { models: [{ id: 'keep' }], api: 'openai-completions' },
    'ai-toolbox': { models: [{ id: 'legacy' }] },
  } };
  const writes = [];
  return {
    doc, writes,
    llm: { listConfigurableProviders: () => Object.keys(doc.providers).map(provider => ({
      provider, settingsNs: 'configured-adapter-id', settingsPath: ['providers', provider],
    })) },
    settings: { async mutate(ns, ops) {
      assert.equal(ns, 'configured-adapter-id');
      writes.push(structuredClone(ops));
      for (const op of ops) {
        assert.equal(op.op, 'set');
        assert.equal(op.path.at(-1), 'models');
        doc.providers[op.path[1]].models = structuredClone(op.value);
      }
    } },
  };
}

test('supplier catalog replaces only gateway models and preserves rich model metadata', async t => {
  const ctx = services();
  const port = await backend(t, (req, res) => {
    assert.equal(req.url, '/v1/models');
    assert.equal(req.headers.authorization, 'Bearer test-key');
    res.setHeader('content-type', 'application/json');
    res.end(JSON.stringify({ data: [
      { id: 'qoder/auto', name: 'Auto' }, null, { id: 42 },
      { id: 'qoder/vision', name: 'Vision · vendor · x0.11', context_length: 128000,
        max_output_tokens: 8192, supports_images: true, supports_reasoning: true,
        supported_efforts: ['none', 'HIGH', 'high', 'unsupported'] },
      { id: 'qoder/text', supports_reasoning: true, supported_efforts: ['unsupported'] },
      { id: 'qoder/text', supports_reasoning: false },
    ] }));
  });
  const result = await modelCatalogSyncer(ctx, { backendPort: port })('test-key');
  assert.deepEqual(result, { status: 'updated', count: 2 });
  const expected = [
    { id: 'qoder/vision', name: 'Vision · x0.11', contextWindow: 128000, maxTokens: 8192,
      input: ['text', 'image'], reasoningEfforts: { off: 'off', high: 'high' } },
    { id: 'qoder/text', input: ['text'], reasoningEfforts: false },
  ];
  assert.deepEqual(ctx.doc.providers['workbuddy-gateway'].models, expected);
  assert.deepEqual(ctx.doc.providers['ai-toolbox'].models, expected);
  assert.equal(ctx.doc.providers['workbuddy-gateway'].apiKeyEnv, 'TEST_REF');
  assert.equal(ctx.doc.providers['workbuddy-gateway'].baseURL, 'http://127.0.0.1:7863/v1');
  assert.deepEqual(ctx.doc.providers['opencode-go'], { models: [{ id: 'keep' }], api: 'openai-completions' });
  assert.equal(ctx.writes.length, 1);
});

test('invalid or failed fetch keeps the current catalog and a subsequent switch recovers', async t => {
  const ctx = services();
  let status = 200, payload;
  const port = await backend(t, (_req, res) => {
    res.writeHead(status, { 'content-type': 'application/json' });
    res.end(JSON.stringify(payload));
  });
  const sync = modelCatalogSyncer(ctx, { backendPort: port });
  for (const invalid of [{ data: [] }, { data: [{ id: 'qoder/auto' }] }, { data: {} }]) {
    payload = invalid;
    await assert.rejects(sync('test-key'), /暂无可用模型|无效的模型目录/);
  }
  status = 401; payload = { error: 'denied' };
  await assert.rejects(sync('bad-key'), /HTTP 401/);
  assert.equal(ctx.writes.length, 0);
  assert.deepEqual(ctx.doc.providers['workbuddy-gateway'].models, [{ id: 'old' }]);
  status = 200; payload = { data: [{ id: 'minimax/test' }] };
  assert.deepEqual(await sync('test-key'), { status: 'updated', count: 1 });
});

test('a newer supplier selection supersedes an in-flight catalog before any settings write', async t => {
  const ctx = services();
  let releaseFirst, firstStarted;
  const started = new Promise(resolve => { firstStarted = resolve; });
  const blocked = new Promise(resolve => { releaseFirst = resolve; });
  let calls = 0;
  const port = await backend(t, async (_req, res) => {
    const call = ++calls;
    if (call === 1) { firstStarted(); await blocked; }
    res.setHeader('content-type', 'application/json');
    res.end(JSON.stringify({ data: [{ id: call === 1 ? 'minimax/old' : 'doubao/latest' }] }));
  });
  const sync = modelCatalogSyncer(ctx, { backendPort: port });
  const first = sync('test-key');
  await started;
  const last = sync('test-key');
  releaseFirst();
  assert.deepEqual(await first, { status: 'superseded' });
  assert.deepEqual(await last, { status: 'updated', count: 1 });
  assert.equal(ctx.writes.length, 1);
  assert.equal(ctx.doc.providers['workbuddy-gateway'].models[0].id, 'doubao/latest');
});

test('cancelled fetch and settings failures report an error without preventing later updates', async t => {
  const ctx = services();
  const port = await backend(t, (_req, res) => res.end('{"data":[{"id":"doubao/test"}]}'));
  const sync = modelCatalogSyncer(ctx, { backendPort: port });
  const controller = new AbortController();
  controller.abort();
  await assert.rejects(sync('test-key', controller.signal), /获取可用模型失败/);
  assert.equal(ctx.writes.length, 0);
  const mutate = ctx.settings.mutate;
  ctx.settings.mutate = async () => { throw new Error('internal configuration detail'); };
  await assert.rejects(sync('test-key'), /DSH 模型目录更新失败/);
  ctx.settings.mutate = mutate;
  assert.equal((await sync('test-key')).status, 'updated');
  ctx.llm.listConfigurableProviders = () => [];
  await assert.rejects(sync('test-key'), /未找到 AI 工具箱网关/);
});

test('native discovery saves regain missing image inputs without changing selections or explicit overrides', async t => {
  const ctx = services();
  const models = [
    { id: 'minimax/vision', name: 'My name', maxTokens: 1234 },
    { id: 'minimax/text' },
    { id: 'minimax/vision', input: ['text'] },
    { id: 'unknown/custom' },
  ];
  ctx.doc.providers['workbuddy-gateway'].models = structuredClone(models);
  let revision = 7, calls = 0;
  const port = await backend(t, (_req, res) => {
    calls++;
    res.end(JSON.stringify({ data: [
      { id: 'minimax/vision', supports_images: true }, { id: 'minimax/text' },
      { id: 'minimax/not-selected', supports_images: true },
    ] }));
  });
  ctx.doc.providers['workbuddy-gateway'].baseURL = `http://127.0.0.1:${port}/v1`;
  ctx.settings.describe = () => [{ ns: 'configured-adapter-id', revision, value: structuredClone(ctx.doc) }];
  ctx.settings.mutate = async (ns, ops, expected) => {
    assert.equal(expected, revision++);
    assert.equal(ns, 'configured-adapter-id');
    ctx.writes.push(ops);
    for (const op of ops) {
      assert.equal(op.path.at(-1), 'input');
      assert.equal(op.path[1], 'workbuddy-gateway');
      ctx.doc.providers[op.path[1]].models[Number(op.path[3])].input = op.value;
    }
  };
  const sync = modelCatalogSyncer(ctx, { backendPort: port });
  await sync.repairInputs('test-key', 'other-namespace');
  assert.equal(calls, 0);
  await sync.repairInputs('test-key', 'configured-adapter-id');
  assert.deepEqual(ctx.doc.providers['workbuddy-gateway'].models, [
    { ...models[0], input: ['text', 'image'] }, { ...models[1], input: ['text'] },
    models[2], models[3],
  ]);
  assert.deepEqual(ctx.doc.providers['opencode-go'].models, [{ id: 'keep' }]);
  assert.equal(ctx.writes.length, 1);
  await sync.repairInputs('test-key');
  assert.equal(ctx.writes.length, 1, 'unknown models do not cause repeated writes');
});

test('input repair refuses stale indices when a newer settings save wins', async t => {
  const ctx = services();
  let release, started;
  const began = new Promise(resolve => { started = resolve; });
  const waiting = new Promise(resolve => { release = resolve; });
  const port = await backend(t, async (_req, res) => {
    started(); await waiting;
    res.end('{"data":[{"id":"old","supports_images":true}]}');
  });
  ctx.doc.providers['workbuddy-gateway'].baseURL = `http://127.0.0.1:${port}/v1`;
  let revision = 1;
  ctx.settings.describe = () => [{ ns: 'configured-adapter-id', revision, value: structuredClone(ctx.doc) }];
  ctx.settings.mutate = async (_ns, _ops, expected) => {
    assert.equal(expected, 1);
    assert.equal(revision, 2);
    throw Object.assign(new Error('conflict'), { code: 'SETTINGS_CONFLICT' });
  };
  const repair = modelCatalogSyncer(ctx, { backendPort: port }).repairInputs('test-key');
  await began;
  ctx.doc.providers['workbuddy-gateway'].models = [{ id: 'new' }];
  revision++;
  release();
  await repair;
  assert.deepEqual(ctx.doc.providers['workbuddy-gateway'].models, [{ id: 'new' }]);
});
