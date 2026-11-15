import test from 'node:test';
import assert from 'node:assert/strict';
import { existsSync } from 'node:fs';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const electron = process.env.LOCALAPPDATA && join(process.env.LOCALAPPDATA, 'Programs', 'DeepSeek Harness', 'DeepSeek Harness.exe');
test('bundle composes one editable pi-ai adapter with the installed DSH configuration engine', {
  skip: process.platform !== 'win32' || !electron || !existsSync(electron),
}, () => {
  const result = spawnSync(electron, [fileURLToPath(new URL('./fixtures/host-bundle-check.cjs', import.meta.url))], {
    encoding: 'utf8', windowsHide: true, timeout: 30_000,
    env: { ...process.env, ELECTRON_RUN_AS_NODE: '1' },
  });
  assert.equal(result.error, undefined);
  assert.equal(result.status, 0, result.stderr || result.stdout);
  assert.deepEqual(JSON.parse(result.stdout), { adapterCount: 1, editable: true, otherProviderPreserved: true });
});

test('installed DSH discovery adoption retains vision after the plugin repairs missing metadata', {
  skip: process.platform !== 'win32' || !electron || !existsSync(electron),
}, () => {
  const result = spawnSync(electron, [fileURLToPath(new URL('./fixtures/host-image-check.cjs', import.meta.url))], {
    encoding: 'utf8', windowsHide: true, timeout: 30_000,
    env: { ...process.env, ELECTRON_RUN_AS_NODE: '1' },
  });
  assert.equal(result.error, undefined);
  assert.equal(result.status, 0, result.stderr || result.stdout);
  const report = JSON.parse(result.stdout);
  assert.equal(report.restoredVision, true);
  assert.equal(report.textOnlyPreserved, true);
  assert.equal(report.consumersRefreshed, true);
});
