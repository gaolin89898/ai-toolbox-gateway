// Exercise the installed DSH discovery and PiAiAdapter with synthetic models
// and credentials. No user profile or running application is read or written.
const assert = require('node:assert/strict');
const { join } = require('node:path');
const { pathToFileURL } = require('node:url');

(async () => {
  const app = join(process.env.LOCALAPPDATA, 'Programs', 'DeepSeek Harness', 'resources', 'app.asar', 'dsh');
  const native = await import(pathToFileURL(join(app, 'node_modules/@deepseek-ai/dsh-llm-pi-ai/lib/index.js')));
  const plugin = await import(pathToFileURL(join(__dirname, '../../lib/index.js')));
  const route = 'workbuddy-gateway', ns = 'llm-pi-ai';
  let raw = { [route]: { api: 'openai-completions', baseURL: 'http://127.0.0.1:7863/v1',
    apiKeyEnv: 'SYNTHETIC_KEY', defaultInput: ['text'], models: [{ id: 'vision', input: ['text', 'image'] }] } };
  let adapter, discover, revision = 0, notified = 0;
  const listeners = new Map();
  const ctx = {
    fiber: { entry: { options: { id: ns } } },
    inject() {}, on(event, fn) { listeners.set(event, [...(listeners.get(event) || []), fn]); },
    emit(event) { if (event === 'llm/adapters-updated') notified++; },
    effect(fn) { return fn(); },
    webServer: { register: () => () => {} },
    credentials: { resolve: async () => ({ value: 'synthetic' }) },
    logger: { warn(message) { throw new Error(message); } },
    get(key) { return key === 'credentials' ? { resolve: async () => ({ value: 'synthetic' }) } : undefined; },
    llm: {
      registerConfigurableProviders: () => ({ replace() {} }),
      registerModelDiscovery(_ns, fn) { discover = fn; },
      registerAdapter(_routes, value) { adapter = value; return { replace() {} }; },
      listConfigurableProviders: () => [{ provider: route, settingsNs: ns, settingsPath: ['providers', route] }],
    },
    settings: {
      describe: () => [{ ns, revision, value: { providers: structuredClone(raw) } }],
      async mutate(_ns, ops, expected) {
        assert.equal(expected, revision);
        const next = structuredClone(raw);
        for (const op of ops) next[route].models[Number(op.path[3])].input = op.value;
        raw = next; revision++;
      },
    },
  };
  globalThis.fetch = async () => new Response(JSON.stringify({ data: [
    { id: 'vision', name: 'Vision', supports_images: true, context_length: 128000 },
    { id: 'text', name: 'Text', supports_images: false },
  ] }), { headers: { 'content-type': 'application/json' } });
  native.apply(ctx, { providers: { get: () => raw } });
  assert.deepEqual((await adapter.resolveModel(route, 'vision')).inputModalities, ['text', 'image']);
  const candidates = await discover({ provider: route, baseURL: raw[route].baseURL, apiKey: 'synthetic' });
  // Reproduce the native Models page's adoption of the discovery candidates.
  raw = { ...raw, [route]: { ...raw[route], models: candidates.map(candidate => ({
    id: candidate.id, name: candidate.name,
    ...(candidate.inputModalities ? { input: candidate.inputModalities } : {}),
  })) } };
  const lostVision = !(await adapter.resolveModel(route, 'vision')).inputModalities.includes('image');
  // Installing the plugin repairs an already saved catalog. Subsequent native
  // saves use DSH's actual document-updated event to trigger the same repair.
  plugin.apply(ctx);
  await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual((await adapter.resolveModel(route, 'vision')).inputModalities, ['text', 'image']);
  assert.deepEqual((await adapter.resolveModel(route, 'text')).inputModalities, ['text']);
  raw = structuredClone(raw);
  delete raw[route].models[0].input;
  revision++;
  for (const fn of listeners.get('settings/document-updated')) fn(ns);
  await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual((await adapter.resolveModel(route, 'vision')).inputModalities, ['text', 'image']);
  assert.equal(notified, 2);
  console.log(JSON.stringify({ lostVision, restoredVision: true, textOnlyPreserved: true, consumersRefreshed: true }));
})().catch(error => { console.error(error); process.exitCode = 1; });
