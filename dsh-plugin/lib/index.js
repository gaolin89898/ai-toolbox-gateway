import { createHandler, PREFIX } from './server.js';
import { modelCatalogSyncer } from './model-sync.js';

export const name = 'dsh-workbuddy-accounts';
export const inject = ['webServer', 'credentials', 'llm', 'settings'];

// cordis.patch.yml 里 pi-ai 网关路由（llm-pi-ai 行）的取密凭证名：pi-ai 每次
// 请求经 ctx.credentials 解析该引用，取到本机 wb2api 管理密钥作 Bearer。
const GATEWAY_KEY_REF = 'WB2API_GATEWAY_API_KEY';

// 把前端带来的管理密钥同步进 DSH 凭证库：与已存值一致时跳过；解析或写入
// 失败静默（面板功能不依赖它，下一轮请求会再试）。
export function keySyncer(credentials) {
  let last = '';
  return async key => {
    if (!key || key === last) return;
    last = key;
    try {
      const resolved = await credentials.resolve(GATEWAY_KEY_REF);
      if (resolved?.value === key) return;
      await credentials.set(GATEWAY_KEY_REF, key);
    } catch { /* 凭证服务暂不可用：下轮请求再试 */ }
  };
}

export function apply(ctx, config = {}) {
  const syncGatewayKey = keySyncer(ctx.credentials);
  const syncModels = modelCatalogSyncer(ctx, config);
  const lifecycle = new AbortController();
  const repairInputs = async ns => {
    if (lifecycle.signal.aborted) return;
    try {
      const key = (await ctx.credentials.resolve(GATEWAY_KEY_REF))?.value;
      if (key) await syncModels.repairInputs(key, ns, lifecycle.signal);
    } catch {
      if (!lifecycle.signal.aborted) ctx.logger.warn('AI 工具箱图片能力同步失败，请重新切换供应应用');
    }
  };
  ctx.on('settings/document-updated', ns => { void repairInputs(ns); });
  void repairInputs();
  const handler = createHandler(config, { onApiKey: syncGatewayKey, onSupplierSaved: syncModels });
  // DSH matches the prefix itself or prefix + '/'; omit the trailing slash.
  ctx.effect(() => {
    const dispose = ctx.webServer.register({ kind: 'prefix', path: PREFIX.slice(0, -1), handler });
    return () => { lifecycle.abort(); return dispose(); };
  },
    'workbuddy-accounts: frontend and local API bridge');
}
