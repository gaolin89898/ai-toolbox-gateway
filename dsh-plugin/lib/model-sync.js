import { catalogEntry, isAutoModel } from './model-catalog.js';

const ROUTES = new Set(['workbuddy-gateway', 'ai-toolbox']);

// Use DSH's live settings service so both the adapter and open model selectors
// see the new catalog. Serialize writes and discard superseded fetches.
export function modelCatalogSyncer(ctx, { backendPort = 7863 } = {}) {
  const { llm, settings } = ctx;
  let generation = 0, pending = Promise.resolve();
  const targets = () => llm.listConfigurableProviders().filter(entry => ROUTES.has(entry.provider));
  const enqueue = task => {
    const result = pending.then(task);
    pending = result.catch(() => {});
    return result;
  };
  const fetchModels = async (apiKey, signal) => {
    const abort = AbortSignal.timeout(60_000);
    let response, data;
    try {
      response = await fetch(`http://127.0.0.1:${backendPort}/v1/models`, {
        headers: { authorization: `Bearer ${apiKey}` }, redirect: 'error',
        signal: signal ? AbortSignal.any([signal, abort]) : abort,
      });
      if (response.ok) data = await response.json();
    } catch {
      throw new Error('获取可用模型失败，请确认账号服务已连接后重新切换供应应用');
    }
    if (!response.ok) throw new Error(`获取可用模型失败（HTTP ${response.status}）`);
    if (!Array.isArray(data?.data)) throw new Error('账号服务返回了无效的模型目录');
    return [...new Map(data.data
      .filter(model => typeof model?.id === 'string' && model.id && !isAutoModel(model))
      .map(model => [model.id, catalogEntry(model)])).values()];
  };
  const sync = (apiKey, signal) => {
    const version = ++generation;
    return enqueue(async () => {
      if (version !== generation) return { status: 'superseded' };
      const routes = targets();
      if (!routes.length) throw new Error('DSH 中未找到 AI 工具箱网关，请确认已启用该供应商');
      const models = await fetchModels(apiKey, signal);
      if (version !== generation) return { status: 'superseded' };
      if (!models.length) throw new Error('所选应用暂无可用模型，请检查是否有已启用的有效账号');
      const namespaces = new Map();
      for (const entry of routes) {
        const ops = namespaces.get(entry.settingsNs) || [];
        ops.push({ op: 'set', path: [...entry.settingsPath, 'models'], value: models });
        namespaces.set(entry.settingsNs, ops);
      }
      try {
        for (const [ns, ops] of namespaces) await settings.mutate(ns, ops);
      } catch {
        throw new Error('DSH 模型目录更新失败，请确认 AI 工具箱网关的模型设置可编辑');
      }
      // The native adapter only republishes route registration when its name
      // or retry policy changes. Refresh consumers of model capabilities too.
      await ctx.emit?.('llm/adapters-updated');
      return { status: 'updated', count: models.length };
    });
  };
  // DSH's endpoint discovery currently adopts id/name/capacity, but omits
  // image capabilities. Saving those candidates then inherits defaultInput:
  // text. Restore only missing input fields; keep the chosen subset, ordering,
  // other edits, and explicit user overrides. Share the supplier-write queue.
  sync.repairInputs = (apiKey, ns, signal) => enqueue(async () => {
    const routes = targets().filter(entry => ns === undefined || entry.settingsNs === ns);
    if (!routes.length) return;
    const views = settings.describe({ redactSecrets: true });
    const missing = new Map();
    for (const entry of routes) {
      const view = views.find(view => view.ns === entry.settingsNs);
      const provider = entry.settingsPath.reduce((value, key) => value?.[key], view?.value);
      // This repair applies only to this plugin's local gateway, not a route
      // the user has repointed at another endpoint.
      if (provider?.baseURL?.replace(/\/+$/, '') !== `http://127.0.0.1:${backendPort}/v1`) continue;
      for (const [index, model] of (provider.models || []).entries()) {
        if (model.input !== undefined) continue;
        const group = missing.get(entry.settingsNs) || { view, entries: [] };
        group.entries.push({ id: model.id, path: [...entry.settingsPath, 'models', String(index), 'input'] });
        missing.set(entry.settingsNs, group);
      }
    }
    if (!missing.size) return;
    const version = generation;
    const catalog = new Map((await fetchModels(apiKey, signal)).map(model => [model.id, model]));
    if (version !== generation) return;
    let changed = false;
    for (const [ns, { view, entries }] of missing) {
      const ops = entries.filter(entry => catalog.has(entry.id))
        .map(entry => ({ op: 'set', path: entry.path, value: catalog.get(entry.id).input }));
      if (!ops.length) continue;
      try { await settings.mutate(ns, ops, view.revision); changed = true; }
      catch (cause) {
        // A newer form save wins; its document-updated event queues a fresh read.
        if (cause?.code !== 'SETTINGS_CONFLICT') throw cause;
      }
    }
    if (changed) await ctx.emit?.('llm/adapters-updated');
  });
  return sync;
}
