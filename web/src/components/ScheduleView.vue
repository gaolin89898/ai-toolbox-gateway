<script setup>
import { computed, reactive, watch } from 'vue';
import { Button as AButton, Message, Progress as AProgress, Switch as ASwitch, Tag as ATag, Tooltip as ATooltip } from '@arco-design/web-vue';
import { IconArrowRight, IconCalendar, IconCheck, IconClockCircle, IconInfoCircle, IconSafe, IconThunderbolt, IconUndo } from '@arco-design/web-vue/es/icon';
import { canCheckin, chinaDate, clockTime, computeNextCheckin, formatHour, formatNumber } from '../useAccounts';
import workbuddyIcon from '../assets/platform-workbuddy.png';
import traeIcon from '../assets/platform-trae.png';
import minimaxIcon from '../assets/platform-minimax.png';
import qoderIcon from '../assets/platform-qoder.png';

const props = defineProps({ model: Object, embedded: Boolean });
const emit = defineEmits(['navigate', 'key']);

const HOURS = Array.from({ length: 24 }, (_, index) => index);
const HOUR_PRESETS = [
  { id: 'morning', label: '每日一次', summary: '09:00', hours: [9] },
  { id: 'twice', label: '早晚两次', summary: '09:00 · 21:00', hours: [9, 21] },
  { id: 'thrice', label: '早中晚三次', summary: '08:00 · 14:00 · 21:00', hours: [8, 14, 21] },
];

const PLATFORM_DEFINITIONS = [
  {
    id: 'workbuddy-cn',
    provider: 'workbuddy',
    name: 'WorkBuddy 国内版',
    icon: workbuddyIcon,
    match: account => (account.provider || 'workbuddy') === 'workbuddy' && account.realm !== 'global' && !account.enterprise,
  },
  {
    id: 'trae',
    provider: 'trae',
    name: 'Trae',
    icon: traeIcon,
    match: account => account.provider === 'trae',
  },
  {
    id: 'minimax',
    provider: 'minimax',
    name: 'MiniMax',
    icon: minimaxIcon,
    match: account => account.provider === 'minimax',
  },
  {
    id: 'qoder',
    provider: 'qoder',
    name: 'Qoder',
    icon: qoderIcon,
    note: '10:00 起',
    match: account => account.provider === 'qoder' && account.checkin_supported !== false,
  },
];

const draft = reactive({
  checkin_enabled: true,
  checkin_hours: [9, 21],
  refresh_after_checkin: true,
  catchup_on_start: true,
});

function normalizeHours(list) {
  return [...new Set((list || []).map(Number).filter(h => Number.isInteger(h) && h >= 0 && h <= 23))].sort((a, b) => a - b);
}

function sameHours(a, b) {
  const left = normalizeHours(a);
  const right = normalizeHours(b);
  return left.length === right.length && left.every((hour, index) => hour === right[index]);
}

function reset() {
  const saved = props.model.schedule;
  if (!saved) return;
  const hours = normalizeHours(saved.checkin_hours);
  Object.assign(draft, {
    checkin_enabled: Boolean(saved.checkin_enabled),
    checkin_hours: hours.length ? hours : [9, 21],
    refresh_after_checkin: saved.refresh_after_checkin !== false,
    catchup_on_start: saved.catchup_on_start !== false,
  });
}

watch(() => props.model.schedule, reset, { immediate: true, deep: true });

const savedSchedule = computed(() => {
  const saved = props.model.schedule;
  if (!saved) return null;
  const hours = normalizeHours(saved.checkin_hours);
  return {
    checkin_enabled: Boolean(saved.checkin_enabled),
    checkin_hours: hours.length ? hours : [9, 21],
    refresh_after_checkin: saved.refresh_after_checkin !== false,
    catchup_on_start: saved.catchup_on_start !== false,
  };
});

const dirtyFields = computed(() => {
  const saved = savedSchedule.value;
  if (!saved) return 0;
  let changed = 0;
  if (draft.checkin_enabled !== saved.checkin_enabled) changed++;
  if (!sameHours(draft.checkin_hours, saved.checkin_hours)) changed++;
  if (draft.refresh_after_checkin !== saved.refresh_after_checkin) changed++;
  if (draft.catchup_on_start !== saved.catchup_on_start) changed++;
  return changed;
});

const isDirty = computed(() => dirtyFields.value > 0);
const saving = computed(() => props.model.busy.has('schedule'));
const checkinAllBusy = computed(() => props.model.busy.has('checkin-all-global') || props.model.busy.has('checkin-all'));

function toggleHour(hour) {
  if (draft.checkin_hours.includes(hour)) {
    if (draft.checkin_hours.length <= 1) {
      Message.info('至少保留 1 个签到时刻');
      return;
    }
    draft.checkin_hours = draft.checkin_hours.filter(item => item !== hour);
    return;
  }
  draft.checkin_hours = normalizeHours([...draft.checkin_hours, hour]);
}

function applyPreset(preset) {
  draft.checkin_hours = [...preset.hours];
}

function save() {
  if (!props.model.schedule || !isDirty.value) return;
  const payload = {
    checkin_enabled: draft.checkin_enabled,
    checkin_hours: normalizeHours(draft.checkin_hours),
    refresh_after_checkin: draft.refresh_after_checkin,
    catchup_on_start: draft.catchup_on_start,
  };
  props.model.saveSchedule(payload).catch(() => {});
}

function triggerCheckinAll() {
  const runner = props.model.checkinAllPlatforms || props.model.checkinAll;
  runner?.().catch(() => {});
}

function openPlatformAccounts(platformId) {
  props.model.selectProvider?.(platformId);
  emit('navigate', 'accounts');
}

const savedNextRun = computed(() => props.model.allNextRun || null);
const previewNextRun = computed(() => (draft.checkin_enabled ? computeNextCheckin(draft.checkin_hours, props.model.now) : null));

const eligibleCount = computed(() => props.model.allEligible.length);
const signedCount = computed(() => props.model.allSigned.length);
const pendingCount = computed(() => props.model.allPending.length);
const completionPercent = computed(() => (eligibleCount.value ? Math.round((signedCount.value / eligibleCount.value) * 100) : 0));

const platformBreakdown = computed(() => {
  const all = props.model.allAccounts || [];
  return PLATFORM_DEFINITIONS.map(def => {
    const matched = all.filter(def.match);
    const eligible = matched.filter(canCheckin);
    const signed = eligible.filter(account => account.checkin_done);
    const pending = eligible.filter(account => !account.checkin_done);
    const disabled = matched.filter(account => account.disabled);
    return {
      ...def,
      total: matched.length,
      eligible: eligible.length,
      signed: signed.length,
      pending: pending.length,
      disabled: disabled.length,
      ratio: eligible.length ? Math.min(1, signed.length / eligible.length) : 0,
    };
  });
});

const activePlatformCount = computed(() => platformBreakdown.value.filter(item => item.eligible > 0).length);
const hasQoderAccounts = computed(() => (props.model.qoderAccounts || []).length > 0);
const hasEarlyHourWithQoder = computed(() => hasQoderAccounts.value && draft.checkin_hours.some(hour => hour < 10));

const skippedSummary = computed(() => {
  const all = props.model.allAccounts || [];
  let globalCount = 0;
  let enterpriseCount = 0;
  let disabledCount = 0;
  let qoderIneligibleCount = 0;
  let unsupportedPlatformCount = 0;

  for (const account of all) {
    if (canCheckin(account)) continue;
    const provider = account.provider || 'workbuddy';
    if (provider === 'antigravity' || provider === 'doubao') unsupportedPlatformCount++;
    else if (provider === 'workbuddy' && account.realm === 'global') globalCount++;
    else if (provider === 'workbuddy' && account.enterprise) enterpriseCount++;
    else if (provider === 'qoder' && account.checkin_supported === false) qoderIneligibleCount++;
    else if (account.disabled) disabledCount++;
  }

  const parts = [];
  if (disabledCount) parts.push(`已停用 ${disabledCount}`);
  if (globalCount) parts.push(`WorkBuddy 国际版 ${globalCount}`);
  if (enterpriseCount) parts.push(`企业版 ${enterpriseCount}`);
  if (qoderIneligibleCount) parts.push(`Qoder 暂无资格 ${qoderIneligibleCount}`);
  if (unsupportedPlatformCount) parts.push(`不支持签到平台 ${unsupportedPlatformCount}`);
  return {
    total: globalCount + enterpriseCount + disabledCount + qoderIneligibleCount + unsupportedPlatformCount,
    parts,
  };
});

function isCheckinLog(text) {
  return /\bcheckin\b|手动全量签到|手动全部签到|手动签到|手动领取每日奖励/i.test(text);
}

function isSuccessfulCheckinLog(text) {
  return (
    (/\bcheckin\b/i.test(text) && /签到成功|今天已签到|幂等/.test(text)) ||
    /^(trae|minimax|qoder):\s*checkin\b.*?(签到成功|完成)/i.test(text)
  );
}

const recentRun = computed(() => [...(props.model.allLogs || [])].reverse().find(entry => isCheckinLog(entry.text)));

const checkinsByDate = computed(() => {
  const counts = new Map();
  for (const entry of props.model.allLogs || []) {
    if (!isSuccessfulCheckinLog(entry.text)) continue;
    const date = chinaDate(entry.ts);
    counts.set(date, (counts.get(date) || 0) + 1);
  }
  return counts;
});

const history = computed(() => Array.from({ length: 7 }, (_, index) => {
  const date = chinaDate(props.model.now - (6 - index) * 86400000);
  const count = checkinsByDate.value.get(date) || 0;
  const today = index === 6;
  const weekday = today
    ? '今天'
    : new Date(date + 'T00:00:00+08:00').toLocaleDateString('zh-CN', { timeZone: 'Asia/Shanghai', weekday: 'short' });
  const label = date.slice(5).replace('-', '/');

  if (today) {
    const ratio = eligibleCount.value ? Math.min(1, signedCount.value / eligibleCount.value) : 0;
    const kind = !eligibleCount.value ? 'muted' : pendingCount.value ? 'warning' : 'success';
    return {
      date,
      today: true,
      weekday,
      label,
      value: eligibleCount.value ? `${signedCount.value} / ${eligibleCount.value}` : '无待签',
      sub: eligibleCount.value ? (pendingCount.value ? `待签 ${pendingCount.value}` : '已全部完成') : '0 个可签账号',
      ratio,
      kind,
    };
  }

  return {
    date,
    today: false,
    weekday,
    label,
    value: count ? `${count} 次` : '--',
    sub: count ? '日志记录' : '无记录',
    ratio: count ? 1 : 0,
    kind: count ? 'success' : 'muted',
  };
}));
</script>

<template>
  <section class="checkin-settings-view" aria-label="签到设置">
    <header class="page-heading checkin-heading">
      <div class="heading-meta">
        <span v-if="isDirty" class="dirty-pill" role="status">
          <i class="status-dot" />有 {{ dirtyFields }} 项未保存更改
        </span>
        <span v-else class="saved-meta">
          <icon-check />{{ model.savedAt ? `上次保存 ${clockTime(model.savedAt).slice(0, 5)}` : '配置已同步 · 北京时间' }}
        </span>
      </div>
      <div class="checkin-actions">
        <a-button v-if="embedded" @click="emit('key')"><template #icon><icon-safe /></template>管理密钥</a-button>
        <a-button type="secondary" @click="emit('navigate', 'logs')">运行日志<icon-arrow-right /></a-button>
        <a-button
          type="primary"
          :disabled="!pendingCount"
          :loading="checkinAllBusy"
          @click="triggerCheckinAll"
        >
          <template #icon><icon-check /></template>
          {{ pendingCount ? `全部签到 (${pendingCount})` : '今日已全签' }}
        </a-button>
      </div>
    </header>

    <section class="stat-strip checkin-strip" aria-label="签到概览">
      <div class="stat-seg">
        <span>自动签到</span>
        <strong :class="model.schedule?.checkin_enabled ? 'accent-text' : ''">
          {{ model.schedule ? (model.schedule.checkin_enabled ? '运行中' : '已关闭') : '--' }}
        </strong>
        <small>{{ model.schedule?.checkin_enabled ? `${(model.schedule?.checkin_hours || []).length} 个定时整点 · 北京时间` : '仅保留手动签到' }}</small>
      </div>
      <i class="stat-divider" />
      <div class="stat-seg">
        <span>下次执行</span>
        <strong>{{ savedNextRun ? savedNextRun.time : '--:--' }}</strong>
        <small>{{ savedNextRun ? `${savedNextRun.day} · 还有 ${savedNextRun.remaining}` : '自动排程已停用' }}</small>
      </div>
      <i class="stat-divider" />
      <div class="stat-seg">
        <span>今日进度</span>
        <strong :class="pendingCount ? 'warning-text' : eligibleCount ? 'accent-text' : ''">
          {{ signedCount }} / {{ eligibleCount }}
        </strong>
        <small>{{ eligibleCount ? `完成率 ${completionPercent}% · 待签 ${pendingCount} 个` : '暂无可签到账号' }}</small>
      </div>
      <i class="stat-divider" />
      <div class="stat-seg">
        <span>参与平台</span>
        <strong>{{ activePlatformCount }} <small class="unit">个</small></strong>
        <small>共 {{ formatNumber(model.allAccounts.length) }} 个账号 · 自动跳过 {{ skippedSummary.total }}</small>
      </div>
    </section>

    <div class="checkin-layout">
      <form class="panel-card checkin-form" @submit.prevent="save">
        <section class="form-block master-block">
          <div class="master-copy">
            <h2>每日自动签到</h2>
            <p>开启后，服务在每天设定整点自动为所有可签到平台依次运行；关闭后不影响手动「全部签到」。</p>
          </div>
          <a-switch
            v-model="draft.checkin_enabled"
            checked-text="开启"
            unchecked-text="关闭"
            aria-label="每日自动签到"
          />
        </section>

        <section :class="['form-block', { 'block-muted': !draft.checkin_enabled }]">
          <div class="block-head">
            <div>
              <h3>签到时刻</h3>
              <small>北京时间（Asia/Shanghai）· 可选择 1 至 24 个整点</small>
            </div>
            <div class="preset-group" role="group" aria-label="常用时刻预设">
              <button
                v-for="preset in HOUR_PRESETS"
                :key="preset.id"
                type="button"
                :class="['preset-btn', { active: sameHours(draft.checkin_hours, preset.hours) }]"
                @click="applyPreset(preset)"
              >
                <span>{{ preset.label }}</span>
                <small>{{ preset.summary }}</small>
              </button>
            </div>
          </div>

          <div class="selected-hours" aria-label="已选签到时刻">
            <span class="selected-label">已选 {{ draft.checkin_hours.length }} 项</span>
            <div class="hour-chips">
              <a-tag
                v-for="hour in draft.checkin_hours"
                :key="hour"
                :closable="draft.checkin_hours.length > 1"
                @close="toggleHour(hour)"
              >
                <icon-clock-circle />{{ formatHour(hour) }}
              </a-tag>
            </div>
          </div>

          <div class="hour-matrix" role="group" aria-label="24 小时整点选择矩阵">
            <button
              v-for="hour in HOURS"
              :key="hour"
              type="button"
              :class="['hour-cell', { selected: draft.checkin_hours.includes(hour), locked: draft.checkin_hours.includes(hour) && draft.checkin_hours.length === 1 }]"
              :aria-pressed="draft.checkin_hours.includes(hour)"
              :title="draft.checkin_hours.includes(hour) && draft.checkin_hours.length === 1 ? '至少保留 1 个签到时刻' : `切换 ${formatHour(hour)}`"
              @click="toggleHour(hour)"
            >
              {{ formatHour(hour) }}
            </button>
          </div>

          <p v-if="!draft.checkin_enabled" class="block-note warning-note">
            <icon-info-circle />当前自动签到已关闭，时刻配置仍可提前编辑，开启总开关并保存后生效。
          </p>
          <p v-else-if="hasEarlyHourWithQoder" class="block-note">
            <icon-info-circle />检测到 Qoder 账号：活动轮次自 10:00 起算，早于 10:00 的时刻对 Qoder 将自动顺延至 10:00 执行。
          </p>
        </section>

        <section class="form-block">
          <div class="block-head">
            <div>
              <h3>执行与容错策略</h3>
              <small>修改保存后即刻热生效，无需重启服务</small>
            </div>
          </div>

          <div class="option-list">
            <label class="option-row">
              <div>
                <strong>签到后自动刷新积分</strong>
                <small>WorkBuddy、Trae、MiniMax 在签到完成后顺手更新最新余额快照与到期批次</small>
              </div>
              <a-switch
                v-model="draft.refresh_after_checkin"
                checked-text="开"
                unchecked-text="关"
                aria-label="签到后自动刷新积分"
              />
            </label>

            <label class="option-row">
              <div>
                <strong>启动时补签当天未签到的账号</strong>
                <small>服务启动或电脑唤醒重启后，自动检测并补齐当日尚未完成签到的可签账号</small>
              </div>
              <a-switch
                v-model="draft.catchup_on_start"
                checked-text="开"
                unchecked-text="关"
                aria-label="启动时补签当天未签到的账号"
              />
            </label>
          </div>
        </section>

        <footer class="form-footer">
          <span :class="['footer-status', { dirty: isDirty }]">
            <template v-if="isDirty">有 {{ dirtyFields }} 项修改尚未保存</template>
            <template v-else><icon-check />当前页面与已生效配置一致</template>
          </span>
          <div class="footer-actions">
            <a-button type="text" :disabled="!isDirty || saving" @click="reset">
              <template #icon><icon-undo /></template>还原
            </a-button>
            <a-button
              type="primary"
              html-type="submit"
              :loading="saving"
              :disabled="!model.schedule || !isDirty"
            >
              <template #icon><icon-check /></template>保存设置
            </a-button>
          </div>
        </footer>
      </form>

      <aside class="checkin-side">
        <section class="command-card" aria-label="调度预览与运行状态">
          <div class="inline-between">
            <span class="command-caption">下次自动执行</span>
            <span :class="['side-badge', { off: !model.schedule?.checkin_enabled }]">
              {{ model.schedule?.checkin_enabled ? '已开启' : '已关闭' }}
            </span>
          </div>

          <div class="command-clock">
            <strong>{{ savedNextRun ? savedNextRun.time : '--:--' }}</strong>
            <span v-if="savedNextRun">{{ savedNextRun.day }}</span>
          </div>
          <p class="command-date">
            {{ savedNextRun ? `${savedNextRun.date} · 剩余 ${savedNextRun.remaining}` : '自动签到已关闭，可随时点击顶部「全部签到」' }}
          </p>

          <div v-if="model.schedule?.checkin_hours?.length" class="command-slots">
            <span v-for="hour in [...model.schedule.checkin_hours].sort((a, b) => a - b)" :key="hour" class="command-slot">
              <icon-calendar />{{ formatHour(hour) }}
            </span>
          </div>

          <div v-if="isDirty" class="command-preview">
            <icon-thunderbolt />
            <span v-if="previewNextRun">保存后预览：下次 {{ previewNextRun.day }} {{ previewNextRun.time }}（共 {{ draft.checkin_hours.length }} 个时刻）</span>
            <span v-else>保存后预览：将停用每日定时签到</span>
          </div>

          <dl class="command-meta">
            <div>
              <dt>上次签到触发</dt>
              <dd>{{ recentRun ? clockTime(recentRun.ts) : '本进程内暂无' }}</dd>
            </div>
            <div>
              <dt>运行时区</dt>
              <dd>北京时间 (UTC+8)</dd>
            </div>
            <div>
              <dt>服务连接</dt>
              <dd :class="model.connected ? 'command-ok' : 'command-warn'">
                <i :class="['status-dot', { offline: !model.connected }]" />{{ model.connected ? '已连接' : '未连接' }}
              </dd>
            </div>
          </dl>
        </section>

        <section class="panel-card coverage-card" aria-label="账号范围与今日进度">
          <header class="ledger-head">
            <div class="ledger-title">
              <h2>签到覆盖与进度</h2>
              <small>全平台共 {{ eligibleCount }} 个可签账号，今日已签 {{ signedCount }} 个</small>
            </div>
          </header>

          <div class="overall-bar">
            <div class="inline-between">
              <span>今日总完成率</span>
              <strong class="accent-text">{{ signedCount }} / {{ eligibleCount }}</strong>
            </div>
            <a-progress :percent="eligibleCount ? Math.min(1, signedCount / eligibleCount) : 0" :show-text="false" color="var(--accent)" size="small" />
          </div>

          <div class="platform-list">
            <button
              v-for="item in platformBreakdown"
              :key="item.id"
              type="button"
              class="platform-row"
              @click="openPlatformAccounts(item.id)"
            >
              <img :src="item.icon" alt="" class="platform-mark" />
              <div class="platform-info">
                <div class="inline-between">
                  <span class="platform-name">
                    <strong>{{ item.name }}</strong>
                    <small v-if="item.note">{{ item.note }}</small>
                  </span>
                  <span class="platform-count-text">
                    <template v-if="item.eligible">{{ item.signed }} / {{ item.eligible }}</template>
                    <template v-else-if="item.total">{{ item.total }} 个不可签</template>
                    <template v-else>未添加</template>
                  </span>
                </div>
                <div class="platform-track">
                  <i :style="{ width: item.eligible ? Math.round(item.ratio * 100) + '%' : '0%' }" :class="{ complete: item.eligible > 0 && item.pending === 0 }" />
                </div>
              </div>
              <icon-arrow-right class="platform-arrow" />
            </button>
          </div>

          <div class="skip-box">
            <strong>自动跳过范围</strong>
            <p v-if="skippedSummary.total">
              当前共跳过 {{ skippedSummary.total }} 个账号（{{ skippedSummary.parts.join(' · ') }}）。
            </p>
            <p v-else>当前账号池内所有已添加账号均符合自动签到条件。</p>
            <p v-if="model.capabilities?.gateway" class="gateway-link-hint">
              连登兑换、旅行巡检与 Token 保活等任务请在
              <button type="button" class="inline-link" @click="emit('navigate', 'schedules')">定时任务</button>
              页面查看。
            </p>
          </div>
        </section>
      </aside>
    </div>

    <section class="panel-card history-card" aria-label="最近 7 天签到记录">
      <header class="ledger-head">
        <div class="ledger-title">
          <h2>最近 7 天签到记录</h2>
          <small>今日按账号实时签到状态统计；历史天数统计本进程运行日志</small>
        </div>
        <a-button type="text" size="small" @click="emit('navigate', 'logs')">查看运行日志<icon-arrow-right /></a-button>
      </header>

      <div class="history-grid">
        <article v-for="day in history" :key="day.date" :class="['day-cell', day.kind, { today: day.today }]">
          <div class="day-head">
            <span>{{ day.weekday }}</span>
            <time>{{ day.label }}</time>
          </div>
          <div class="day-bar">
            <i :style="{ width: Math.round(day.ratio * 100) + '%' }" />
          </div>
          <div class="day-foot">
            <strong>{{ day.value }}</strong>
            <small>{{ day.sub }}</small>
          </div>
        </article>
      </div>
    </section>
  </section>
</template>

<style scoped>
.checkin-settings-view {
  display: grid;
  gap: 14px;
}

.checkin-heading {
  justify-content: space-between;
  margin-bottom: 0;
  gap: 12px;
  flex-wrap: wrap;
}

.heading-meta {
  display: flex;
  align-items: center;
  min-height: 28px;
}

.saved-meta,
.dirty-pill {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 11px;
  border-radius: 999px;
  padding: 4px 10px;
}

.saved-meta {
  color: var(--muted);
  background: var(--surface);
  border: 1px solid var(--line);
}

.saved-meta .arco-icon {
  color: var(--accent);
}

.dirty-pill {
  color: var(--warning);
  background: var(--warning-soft);
  font-weight: 500;
}

.dirty-pill .status-dot {
  background: var(--warning);
  margin-right: 0;
}

.checkin-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-left: auto;
}

.checkin-strip strong {
  font-variant-numeric: tabular-nums;
}

.checkin-strip .unit {
  font-size: 12px;
  font-weight: 400;
  color: var(--muted);
}

.checkin-layout {
  display: grid;
  grid-template-columns: minmax(0, 1.72fr) minmax(300px, 1fr);
  gap: 14px;
  align-items: start;
}

.checkin-form {
  padding: 0;
  gap: 0;
  overflow: hidden;
}

.form-block {
  padding: 20px 22px;
  border-top: 1px solid var(--line);
  transition: opacity 0.16s ease;
}

.form-block:first-child {
  border-top: 0;
}

.form-block.block-muted {
  opacity: 0.72;
}

.master-block {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 18px;
}

.master-copy h2 {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
}

.master-copy p {
  margin-top: 4px;
  color: var(--muted);
  font-size: 11.5px;
  line-height: 1.6;
}

.block-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.block-head h3 {
  font-size: 13.5px;
  font-weight: 600;
}

.block-head small {
  display: block;
  margin-top: 2px;
  color: var(--muted);
  font-size: 11px;
}

.preset-group {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.preset-btn {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  border: 1px solid var(--line);
  background: var(--surface-secondary);
  color: var(--secondary);
  border-radius: 8px;
  padding: 5px 10px;
  font-size: 11px;
  transition: border-color 0.14s ease, background-color 0.14s ease, color 0.14s ease;
}

.preset-btn small {
  color: var(--muted);
  font-size: 10px;
  font-family: Inter, Consolas, monospace;
}

.preset-btn:hover {
  border-color: var(--accent);
  color: var(--ink);
}

.preset-btn.active {
  border-color: var(--accent);
  background: var(--accent-soft);
  color: var(--accent);
  font-weight: 500;
}

.preset-btn.active small {
  color: var(--accent);
}

.selected-hours {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  margin-top: 14px;
}

.selected-label {
  font-size: 11px;
  color: var(--muted);
  white-space: nowrap;
}

.hour-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.hour-chips :deep(.arco-tag) {
  color: var(--accent);
  background: var(--accent-soft);
  border-radius: 7px;
  height: 28px;
  font-size: 12px;
  font-variant-numeric: tabular-nums;
  padding: 0 10px;
}

.hour-chips :deep(.arco-tag .arco-icon) {
  margin-right: 4px;
}

.hour-matrix {
  display: grid;
  grid-template-columns: repeat(8, minmax(0, 1fr));
  gap: 6px;
  margin-top: 14px;
}

.hour-cell {
  height: 34px;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--surface-secondary);
  color: var(--secondary);
  font-family: Inter, 'JetBrains Mono', Consolas, monospace;
  font-size: 11.5px;
  font-variant-numeric: tabular-nums;
  transition: border-color 0.12s ease, background-color 0.12s ease, color 0.12s ease;
}

.hour-cell:focus-visible {
  outline-offset: 1px;
}

.hour-cell:hover {
  border-color: var(--accent);
  color: var(--ink);
}

.hour-cell.selected {
  background: var(--host-button, rgb(var(--primary-6)));
  border-color: var(--host-button, rgb(var(--primary-6)));
  color: var(--host-button-text, #fff);
  font-weight: 600;
}

.hour-cell.locked {
  cursor: default;
}

.block-note {
  display: flex;
  align-items: center;
  gap: 7px;
  margin-top: 12px;
  padding: 8px 11px;
  border-radius: 8px;
  background: var(--surface-secondary);
  color: var(--muted);
  font-size: 11px;
}

.block-note.warning-note {
  background: var(--warning-soft);
  color: var(--warning);
}

.block-note .arco-icon {
  flex-shrink: 0;
  font-size: 13px;
}

.option-list {
  display: grid;
  gap: 0;
  margin-top: 10px;
}

.option-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 12px 0;
  border-bottom: 1px solid var(--line);
  cursor: pointer;
}

.option-row:last-child {
  border-bottom: 0;
  padding-bottom: 0;
}

.option-row strong {
  display: block;
  font-size: 12.5px;
  font-weight: 500;
}

.option-row small {
  display: block;
  margin-top: 2px;
  color: var(--muted);
  font-size: 11px;
  line-height: 1.5;
}

.form-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  border-top: 1px solid var(--line);
  padding: 13px 22px;
  background: var(--surface-secondary);
}

.footer-status {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 11px;
  color: var(--muted);
}

.footer-status .arco-icon {
  color: var(--accent);
}

.footer-status.dirty {
  color: var(--warning);
  font-weight: 500;
}

.footer-actions {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-shrink: 0;
}

.checkin-side {
  display: grid;
  gap: 14px;
  min-width: 0;
}

.command-card {
  background: #15161a;
  border: 1px solid #2b2d35;
  color: #f2f2f4;
  border-radius: 14px;
  padding: 20px;
  display: grid;
  gap: 10px;
}

.command-caption {
  color: #979aa4;
  font-size: 11.5px;
}

.side-badge.off {
  color: #979aa4;
  background: #272a32;
}

.side-badge.off::before {
  background: #777a85;
}

.command-clock {
  display: flex;
  align-items: baseline;
  gap: 8px;
}

.command-clock strong {
  font-family: Inter, 'Noto Sans SC', sans-serif;
  font-size: 40px;
  line-height: 1.15;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}

.command-clock span {
  font-size: 12px;
  color: #979aa4;
}

.command-date {
  color: #979aa4;
  font-size: 11px;
}

.command-slots {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.command-slot {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 3px 8px;
  border-radius: 6px;
  background: #1e2026;
  border: 1px solid #2b2d35;
  color: #c6c8d0;
  font-size: 11px;
  font-variant-numeric: tabular-nums;
}

.command-slot .arco-icon {
  color: #91a6ff;
  font-size: 12px;
}

.command-preview {
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 8px 10px;
  border-radius: 8px;
  background: #23283a;
  color: #b8c3ff;
  font-size: 11px;
}

.command-preview .arco-icon {
  flex-shrink: 0;
}

.command-meta {
  margin: 4px 0 0;
  padding-top: 8px;
  border-top: 1px solid #2b2d35;
  display: grid;
  gap: 0;
}

.command-meta > div {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 10px;
  padding: 7px 0;
  font-size: 11px;
}

.command-meta dt {
  color: #979aa4;
}

.command-meta dd {
  margin: 0;
  text-align: right;
  color: #e4e5ea;
  font-variant-numeric: tabular-nums;
}

.command-ok {
  color: #91a6ff !important;
}

.command-ok .status-dot {
  background: #91a6ff;
}

.command-warn {
  color: #dfb772 !important;
}

.coverage-card {
  gap: 12px;
}

.coverage-card .ledger-head {
  margin-bottom: 0;
}

.overall-bar {
  display: grid;
  gap: 6px;
}

.overall-bar span {
  font-size: 11px;
  color: var(--muted);
}

.overall-bar strong {
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}

.platform-list {
  display: grid;
  gap: 6px;
}

.platform-row {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  padding: 9px 10px;
  border: 1px solid var(--line);
  border-radius: 10px;
  background: var(--surface-secondary);
  color: var(--ink);
  text-align: left;
  transition: border-color 0.14s ease, background-color 0.14s ease;
}

.platform-row:hover {
  border-color: var(--accent);
  background: var(--surface);
}

.platform-info {
  flex: 1;
  min-width: 0;
  display: grid;
  gap: 5px;
}

.platform-name {
  display: inline-flex;
  align-items: baseline;
  gap: 6px;
  min-width: 0;
}

.platform-name strong {
  font-size: 12px;
  font-weight: 500;
}

.platform-name small {
  color: var(--muted);
  font-size: 10px;
}

.platform-count-text {
  font-size: 11px;
  color: var(--secondary);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.platform-track {
  height: 4px;
  border-radius: 2px;
  background: var(--line);
  overflow: hidden;
}

.platform-track i {
  display: block;
  height: 100%;
  border-radius: 2px;
  background: var(--warning);
  transition: width 0.2s ease;
}

.platform-track i.complete {
  background: var(--accent);
}

.platform-arrow {
  color: var(--muted);
  font-size: 12px;
  flex-shrink: 0;
}

.skip-box {
  padding-top: 10px;
  border-top: 1px solid var(--line);
  font-size: 11px;
  color: var(--muted);
  line-height: 1.6;
}

.skip-box strong {
  display: block;
  color: var(--secondary);
  font-weight: 500;
  margin-bottom: 2px;
}

.gateway-link-hint {
  margin-top: 6px;
}

.inline-link {
  border: 0;
  padding: 0 2px;
  background: transparent;
  color: var(--accent);
  font-size: inherit;
  text-decoration: underline;
  text-underline-offset: 2px;
}

.history-card .ledger-head {
  margin-bottom: 0;
}

.history-grid {
  display: grid;
  grid-template-columns: repeat(7, minmax(0, 1fr));
  gap: 8px;
}

.day-cell {
  background: var(--surface-secondary);
  border: 1px solid var(--line);
  border-radius: 10px;
  padding: 11px 12px;
  display: grid;
  gap: 8px;
}

.day-cell.today {
  background: var(--surface);
  border-color: var(--accent);
}

.day-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 4px;
  font-size: 11px;
}

.day-head span {
  color: var(--muted);
}

.day-cell.today .day-head span {
  color: var(--accent);
  font-weight: 600;
}

.day-head time {
  color: var(--secondary);
  font-variant-numeric: tabular-nums;
}

.day-bar {
  height: 4px;
  border-radius: 2px;
  background: var(--line);
  overflow: hidden;
}

.day-bar i {
  display: block;
  height: 100%;
  border-radius: 2px;
  background: var(--muted);
}

.day-cell.success .day-bar i {
  background: var(--accent);
}

.day-cell.warning .day-bar i {
  background: var(--warning);
}

.day-foot {
  display: grid;
  gap: 1px;
}

.day-foot strong {
  font-size: 13px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}

.day-cell.success .day-foot strong {
  color: var(--accent);
}

.day-cell.warning .day-foot strong {
  color: var(--warning);
}

.day-cell.muted .day-foot strong {
  color: var(--muted);
  font-weight: 500;
}

.day-foot small {
  font-size: 10px;
  color: var(--muted);
}

@media (max-width: 1180px) {
  .checkin-layout {
    grid-template-columns: minmax(0, 1fr);
  }

  .checkin-side {
    grid-template-columns: 1fr 1fr;
    align-items: start;
  }
}

@media (max-width: 820px) {
  .checkin-actions {
    width: 100%;
    margin-left: 0;
  }

  .checkin-actions .arco-btn:last-child {
    margin-left: auto;
  }

  .checkin-strip {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 10px 0;
    padding: 12px 4px;
  }

  .checkin-strip .stat-divider {
    display: none;
  }

  .checkin-strip .stat-seg {
    padding: 2px 14px;
    border-left: 1px solid var(--line);
  }

  .checkin-strip .stat-seg:nth-of-type(odd) {
    border-left: 0;
  }

  .checkin-side {
    grid-template-columns: minmax(0, 1fr);
  }

  .form-block {
    padding: 16px;
  }

  .form-footer {
    padding: 12px 16px;
    flex-wrap: wrap;
  }

  .footer-actions {
    margin-left: auto;
  }

  .hour-matrix {
    grid-template-columns: repeat(6, minmax(0, 1fr));
  }

  .history-grid {
    grid-template-columns: repeat(4, minmax(0, 1fr));
  }
}

@media (max-width: 480px) {
  .hour-matrix {
    grid-template-columns: repeat(4, minmax(0, 1fr));
  }

  .history-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
</style>
