<script setup>
import { computed } from 'vue';
import { Tooltip as ATooltip } from '@arco-design/web-vue';
import { IconInfoCircle } from '@arco-design/web-vue/es/icon';

const props = defineProps({
  account: { type: Object, required: true },
  mobile: { type: Boolean, default: false }
});

const groups = computed(() => props.account?.quota_groups || []);

function quotaDate(value) {
  if (!value) return '';
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleDateString('zh-CN', { timeZone: 'Asia/Shanghai', month: '2-digit', day: '2-digit' });
}

function quotaTime(value) {
  if (!value) return '';
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleTimeString('zh-CN', { timeZone: 'Asia/Shanghai', hour: '2-digit', minute: '2-digit', hour12: false });
}

function fullResetTime(value) {
  if (!value) return '';
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai' });
}

function getDotClass(name = '') {
  const n = String(name).toLowerCase();
  if (n.includes('gemini')) return 'dot-gemini';
  if (n.includes('claude') || n.includes('gpt')) return 'dot-claude';
  return 'dot-default';
}

function getPercent(remaining) {
  const val = Number(remaining);
  if (!Number.isFinite(val)) return 0;
  return Math.min(100, Math.max(0, Math.round(val * 100)));
}

function getLevelClass(remaining) {
  if (props.account?.disabled) return 'level-disabled';
  const pct = getPercent(remaining);
  if (pct >= 60) return 'level-high';
  if (pct >= 20) return 'level-mid';
  return 'level-low';
}

function getBarColor(remaining) {
  if (props.account?.disabled) return 'var(--muted)';
  const pct = getPercent(remaining);
  if (pct >= 60) return '#10b981';
  if (pct >= 20) return '#f59e0b';
  return '#ef4444';
}
</script>

<template>
  <div
    v-if="groups.length"
    class="ag-quota-grid"
    :class="{ 'is-mobile': mobile, 'single-group': groups.length === 1 }"
    :style="groups.length > 2 && !mobile ? { gridTemplateColumns: `repeat(${groups.length}, minmax(0, 1fr))` } : undefined"
  >
    <div v-for="g in groups" :key="g.name" class="ag-quota-group">
      <!-- 模型组头部：指示圆点 + 名称 + 悬浮详情 -->
      <div class="ag-group-head">
        <span class="ag-group-dot" :class="getDotClass(g.name)" />
        <span class="ag-group-name">{{ g.name }}</span>
        <a-tooltip v-if="g.description" :content="g.description" position="top" mini>
          <icon-info-circle class="ag-group-info" />
        </a-tooltip>
      </div>

      <!-- 配额滑动条列表（周额度 / 5小时） -->
      <div class="ag-bucket-list">
        <div
          v-for="b in g.buckets"
          :key="b.id || b.name"
          class="ag-bucket-item"
          :title="`${g.name} · ${b.window === 'weekly' ? '周额度' : '5 小时'}: 剩余 ${getPercent(b.remaining)}%${b.reset_at ? '（重置时间：' + fullResetTime(b.reset_at) + '）' : ''}`"
        >
          <div class="ag-bucket-meta">
            <span class="ag-bucket-label">{{ b.window === 'weekly' ? '周额度' : '5 小时' }}</span>
            <div class="ag-bucket-values">
              <span class="ag-bucket-val" :class="getLevelClass(b.remaining)">{{ getPercent(b.remaining) }}%</span>
              <span v-if="b.reset_at" class="ag-bucket-reset">
                {{ b.window === 'weekly' ? quotaDate(b.reset_at) : quotaTime(b.reset_at) }} 重置
              </span>
            </div>
          </div>
          <div class="ag-track">
            <div
              class="ag-fill"
              :style="{ width: `${getPercent(b.remaining)}%`, backgroundColor: getBarColor(b.remaining) }"
            />
          </div>
        </div>
      </div>
    </div>
  </div>

  <div v-else class="ag-quota-empty">
    <span>{{ account?.quota_known ? '暂无配额数据' : '未获取' }}</span>
  </div>
</template>

<style scoped>
.ag-quota-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 24px;
  align-items: center;
  width: 100%;
}

.ag-quota-grid.single-group {
  grid-template-columns: 1fr;
  max-width: 260px;
}

.ag-quota-grid.is-mobile {
  grid-template-columns: 1fr;
  gap: 12px;
}

.ag-quota-group {
  display: flex;
  flex-direction: column;
  gap: 7px;
  min-width: 0;
  position: relative;
}

/* 桌面端：在相邻组之间显示一条超轻的竖向分隔线 */
@media (min-width: 821px) {
  .ag-quota-grid:not(.is-mobile):not(.single-group) .ag-quota-group + .ag-quota-group::before {
    content: '';
    position: absolute;
    left: -12px;
    top: 2px;
    bottom: 2px;
    width: 1px;
    background: var(--line);
  }
}

.ag-group-head {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.ag-group-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  flex-shrink: 0;
}

.dot-gemini {
  background: #2563eb;
  box-shadow: 0 0 0 2px rgba(37, 99, 235, 0.18);
}

.dot-claude {
  background: #8b5cf6;
  box-shadow: 0 0 0 2px rgba(139, 92, 246, 0.18);
}

.dot-default {
  background: var(--accent);
  box-shadow: 0 0 0 2px var(--accent-soft);
}

.ag-group-name {
  font-size: 11.5px;
  font-weight: 600;
  color: var(--ink);
  letter-spacing: 0.15px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.ag-group-info {
  font-size: 12px;
  color: var(--muted);
  cursor: pointer;
  flex-shrink: 0;
  transition: color 0.15s ease;
}

.ag-group-info:hover {
  color: var(--accent);
}

.ag-bucket-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.ag-bucket-item {
  display: flex;
  flex-direction: column;
  gap: 3px;
  cursor: default;
}

.ag-bucket-meta {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
  line-height: 1.25;
}

.ag-bucket-label {
  font-size: 11px;
  color: var(--secondary);
  font-weight: 500;
}

.ag-bucket-values {
  display: inline-flex;
  align-items: baseline;
  gap: 5px;
  font-variant-numeric: tabular-nums;
}

.ag-bucket-val {
  font-size: 11.5px;
  font-weight: 600;
}

.level-high {
  color: #10b981;
}

.level-mid {
  color: #f59e0b;
}

.level-low {
  color: #ef4444;
}

.level-disabled {
  color: var(--muted);
}

.ag-bucket-reset {
  font-size: 10px;
  color: var(--muted);
  white-space: nowrap;
}

.ag-track {
  width: 100%;
  height: 4px;
  background: var(--line);
  border-radius: 999px;
  overflow: hidden;
}

.ag-fill {
  height: 100%;
  border-radius: 999px;
  transition: width 0.35s cubic-bezier(0.4, 0, 0.2, 1);
}

.ag-quota-empty {
  color: var(--muted);
  font-size: 11px;
}
</style>
