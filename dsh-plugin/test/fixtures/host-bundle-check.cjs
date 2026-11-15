// Read the installed DSH engine, but compose and edit an isolated temporary
// profile. No running application, account, credential, or user setting is read.
const fs = require('node:fs');
const { join } = require('node:path');
const { tmpdir } = require('node:os');
const { pathToFileURL } = require('node:url');
const { isDeepStrictEqual } = require('node:util');
const assert = require('node:assert/strict');

(async () => {
  const app = join(process.env.LOCALAPPDATA, 'Programs', 'DeepSeek Harness', 'resources', 'app.asar', 'dsh');
  const load = p => import(pathToFileURL(join(app, 'node_modules', ...p.split('/'))));
  const boot = await load('@deepseek-ai/dsh-app-boot/lib/index.js');
  const { entryListSchema } = await load('@deepseek-ai/cordis-plugin-include/lib/index.js');
  const yaml = (await load('js-yaml/index.js')).default;
  const YAML = await load('yaml/dist/index.js');
  const bundle = yaml.load(fs.readFileSync(join(__dirname, '../../cordis.patch.yml'), 'utf8'), { schema: entryListSchema });
  const gateway = bundle.find(row => row.id === 'llm-pi-ai')?.config?.providers?.['workbuddy-gateway'];
  assert.ok(gateway, 'Gateway must patch the existing adapter, rather than insert another instance');
  const dir = fs.mkdtempSync(join(tmpdir(), 'dsh-model-bundle-test-'));
  try {
    fs.writeFileSync(join(dir, 'package.json'), JSON.stringify({ name: 'dsh-model-sync-test', private: true,
      dsh: { profile: { bundles: ['@deepseek-ai/dsh-base'] } } }));
    const other = { api: 'openai-completions', baseURL: 'https://example.invalid/v1', models: [{ id: 'keep' }] };
    const patch = [{ id: 'llm-pi-ai', name: '@deepseek-ai/dsh-llm-pi-ai', config: { providers: {
      'opencode-go': other, 'workbuddy-gateway': gateway,
    } } }];
    const patchPath = join(dir, 'cordis.patch.yml');
    fs.writeFileSync(patchPath, yaml.dump(patch));
    const loaded = boot.loadProfileDirectory('dsh', dir, join(app, 'package.json'));
    loaded.layers.push({ packageName: 'dsh-workbuddy-accounts', patches: bundle });
    const context = { dir, patchPath, home: dir, installAnchor: join(app, 'package.json'), overlays: [] };
    // Use a separate absent home patch; the profile is the only user layer.
    context.home = join(dir, 'home');
    const rows = boot.composeEntries([boot.readProfilePatches('dsh', context, loaded)]);
    const adapters = rows.filter(row => row.id === 'llm-pi-ai');
    assert.equal(adapters.length, 1, 'Duplicate adapter rows make ConfigEditor reject all model edits');
    const row = adapters[0];
    const next = structuredClone(row.config);
    next.providers['workbuddy-gateway'].models = [{ id: 'minimax/MiniMax-M3', contextWindow: 512000,
      maxTokens: 128000, input: ['text', 'image'], reasoningEfforts: false }];
    // Exercise the same YAML round trip and composition preflight used by
    // DSH ConfigEditor.edit before it accepts a live settings mutation.
    const doc = YAML.parseDocument(fs.readFileSync(patchPath, 'utf8'));
    const index = doc.contents.items.findLastIndex((item, i) => YAML.isMap(item)
      && doc.getIn([i, 'id']) === row.id && !item.has('insert')
      && (!item.has('name') || doc.getIn([i, 'name']) === row.name));
    doc.setIn([index, 'config'], doc.createNode(next));
    const candidate = { ...loaded, patches: yaml.load(String(doc), { schema: entryListSchema }) };
    const after = boot.composeEntries([boot.readProfilePatches('dsh', context, candidate)])
      .find(entry => entry.id === row.id)?.config;
    const editable = isDeepStrictEqual(after, next);
    const otherProviderPreserved = isDeepStrictEqual(after?.providers?.['opencode-go'], other);
    assert.ok(editable, 'DSH would reject the catalog as overridden by another configuration layer');
    assert.ok(otherProviderPreserved);
    console.log(JSON.stringify({ adapterCount: adapters.length, editable, otherProviderPreserved }));
  } finally {
    for (const file of ['cordis.patch.yml', 'package.json']) fs.unlinkSync(join(dir, file));
    fs.rmdirSync(dir);
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
