// Shared model metadata for live DSH updates and release catalog generation.
const VENDOR_CREDIT = /\s*\u00b7\s*[^\u00b7]+\s*\u00b7\s*(x[0-9.]+)\s*$/u;
const VENDOR_ONLY = /\s*\u00b7\s*[^\u00b7]+\s*$/u;

/** 去掉上游 name 的厂商标识段，保留倍率；无有效名字时返回空串。 */
export function cleanName(raw, id) {
  let name = String(raw || '').trim();
  if (!name) return '';
  if (VENDOR_CREDIT.test(name)) name = name.replace(VENDOR_CREDIT, ' \u00b7 $1');
  else name = name.replace(VENDOR_ONLY, '');
  name = name.trim();
  if (!name) return '';
  const bare = bareId(id);
  if (name === id || name === bare) return '';
  return name;
}

/** 去掉 realm / 供应商前缀的裸模型 id。 */
export function bareId(id) {
  if (id.includes(':')) return id.slice(id.indexOf(':') + 1);
  return id.includes('/') ? id.split('/').pop() : id;
}

/** 自动选模型的条目（Auto / auto-chat / default-model 等）。 */
export function isAutoModel(model) {
  const bare = bareId(model.id).toLowerCase();
  if (bare === 'auto' || bare.endsWith('-auto') || bare.startsWith('auto-')) return true;
  const head = String(model.name || '').split('\u00b7')[0].trim().toLowerCase();
  return head === 'auto';
}

/** 把 /v1/models 条目映射为 DSH 供应商目录条目。 */
export function catalogEntry(model) {
  const entry = { id: model.id };
  const name = cleanName(model.name, model.id);
  if (name) entry.name = name;
  if (model.context_length) entry.contextWindow = model.context_length;
  if (model.max_output_tokens) entry.maxTokens = model.max_output_tokens;
  entry.input = model.supports_images ? ['text', 'image'] : ['text'];
  // DSH 的 reasoningEfforts 只接受 off/minimal/low/medium/high/xhigh/max；
  // 网关目录里的 none 映射成 off，其余不认识的档位直接剔除，避免整段配置校验失败。
  const SUPPORTED_EFFORTS = new Set(['off', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max']);
  const efforts = model.supports_reasoning && Array.isArray(model.supported_efforts) && model.supported_efforts.length
    ? [...new Set(model.supported_efforts.map(level => String(level).toLowerCase() === 'none' ? 'off' : String(level).toLowerCase()).filter(level => SUPPORTED_EFFORTS.has(level)))]
    : null;
  entry.reasoningEfforts = efforts?.length ? Object.fromEntries(efforts.map(level => [level, level])) : false;
  return entry;
}
