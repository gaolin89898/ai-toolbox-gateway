<script setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { Button as AButton, Empty as AEmpty, InputNumber as AInputNumber, Message, Modal as AModal, Switch as ASwitch, Tag as ATag } from '@arco-design/web-vue';
import {
  IconArrowRight,
  IconCheck,
  IconClockCircle,
  IconEdit,
  IconInfoCircle,
  IconPauseCircle,
  IconRefresh,
  IconSafe,
  IconUndo,
  IconUp,
} from '@arco-design/web-vue/es/icon';
import { clockTime, formatHour } from '../useAccounts';

const props = defineProps({ model: Object, embedded: Boolean });
const emit = defineEmits(['navigate', 'key']);

const HOURS = Array.from({ length: 24 }, (_, index) => index);
const INTERVAL_PRESETS = [5, 10, 15, 30, 60];

const loading = ref(false);
const saving = ref(false);
const config = ref(null);
const draft = ref(null);
const savedSnapshot = ref(null);
const savedAt = ref(null);
const lastRuns = ref({});
const pauseConfirm = ref(false);
const statusFilter = ref('all');
const expandedId = ref('');

function normalizeHours(list, fallback = [9]) {
  const cleaned = [...new Set((list || []).map(Number).filter(h => Number.isInteger(h) && h >= 0 && h <= 23))].sort((a, b) => a - b);
  return cleaned.length ? cleaned : [...fallback];
}

function sameHours(a, b) {
  const left = normalizeHours(a, []);
  const right = normalizeHours(b, []);
  return left.length === right.length && left.every((hour, index) => hour === right[index]);
}

function cloneDraft(source) {
  if (!source) return null;
  return {
    checkin_enabled: Boolean(source.checkin_enabled),
    checkin_hours: normalizeHours(source.checkin_hours, [9]),
    travel_enabled: Boolean(source.travel_enabled),
    travel_hours: normalizeHours(source.travel_hours, [9, 21]),
    activity_enabled: Boolean(source.activity_enabled),
    activity_hours: normalizeHours(source.activity_hours, [10]),
    keepalive_enabled: Boolean(source.keepalive_enabled),
    keepalive_hours: normalizeHours(source.keepalive_hours, [22]),
    blackcat_enabled: Boolean(source.blackcat_enabled),
    blackcat_hours: normalizeHours(source.blackcat_hours, [23]),
    growth_enabled: Boolean(source.growth_enabled),
    growth_hours: normalizeHours(source.growth_hours, [1]),
    streak_enabled: Boolean(source.streak_enabled),
    task_sweep_enabled: Boolean(source.task_sweep_enabled),
    task_sweep_minutes: Math.max(1, Number(source.task_sweep_minutes) || 15),
    balance_refresh_enabled: Boolean(source.balance_refresh_enabled),
    balance_refresh_minutes: Math.max(1, Number(source.balance_refresh_minutes) || 5),
  };
}

const families = computed(() => {
  const d = draft.value;
  if (!d) return [];
  return [
    {
      id: 'checkin',
      group: 'checkin',
      name: '每日签到',
      desc: 'WorkBuddy、Trae、MiniMax 同批执行；Qoder 活动轮次顺延至 10:00 起',
      platforms: ['WorkBuddy', 'Trae', 'MiniMax', 'Qoder'],
      plan: d.checkin_hours,
      enabled: d.checkin_enabled,
      statusKey: 'checkin',
      readonly: true,
    },
    {
      id: 'streak',
      group: 'checkin',
      name: '连登管家',
      desc: '签到完成后自动兑换已达标连登档位并抽完可用抽奖次数',
      platforms: ['WorkBuddy'],
      plan: [],
      enabled: d.streak_enabled,
      key: 'streak',
      statusKey: 'streak',
      follows: 'checkin',
    },
    {
      id: 'travel',
      group: 'hourly',
      name: '旅行巡检',
      desc: '自动执行一趟派出与领奖闭环',
      platforms: ['WorkBuddy'],
      plan: d.travel_hours,
      hoursKey: 'travel_hours',
      defaultHours: [9, 21],
      enabled: d.travel_enabled,
      key: 'travel',
      statusKey: 'travel',
    },
    {
      id: 'activity',
      group: 'hourly',
      name: '活跃上报',
      desc: '点亮每日连登状态并解锁领养前置条件',
      platforms: ['WorkBuddy'],
      plan: d.activity_hours,
      hoursKey: 'activity_hours',
      defaultHours: [10],
      enabled: d.activity_enabled,
      key: 'activity',
      statusKey: 'activity',
    },
    {
      id: 'keepalive',
      group: 'hourly',
      name: 'Token 保活',
      desc: '定期刷新登录凭证，会话彻底失效时自动停用账号',
      platforms: ['WorkBuddy'],
      plan: d.keepalive_hours,
      hoursKey: 'keepalive_hours',
      defaultHours: [22],
      enabled: d.keepalive_enabled,
      key: 'keepalive',
      statusKey: 'keepalive',
    },
    {
      id: 'blackcat',
      group: 'hourly',
      name: '夜猫子',
      desc: '在 23:00–08:00 夜间窗口内自动补足对话次数',
      hint: '建议保持在 23:00–07:00 夜间计数窗口内',
      platforms: ['WorkBuddy'],
      plan: d.blackcat_hours,
      hoursKey: 'blackcat_hours',
      defaultHours: [23],
      enabled: d.blackcat_enabled,
      key: 'blackcat',
      statusKey: 'blackcat',
    },
    {
      id: 'growth',
      group: 'hourly',
      name: '成长任务',
      desc: '每日到点自动扫描并执行全部可执行待办',
      platforms: ['WorkBuddy'],
      plan: d.growth_hours,
      hoursKey: 'growth_hours',
      defaultHours: [1],
      enabled: d.growth_enabled,
      key: 'growth',
      statusKey: 'growth',
    },
    {
      id: 'sweep',
      group: 'interval',
      name: '任务补领清扫',
      desc: '周期领取「已达标未领奖」的成长任务奖励（幂等安全）',
      platforms: ['WorkBuddy'],
      interval: d.task_sweep_minutes,
      intervalKey: 'task_sweep_minutes',
      defaultInterval: 15,
      enabled: d.task_sweep_enabled,
      key: 'task_sweep',
      statusKey: 'sweep',
    },
    {
      id: 'balance',
      group: 'interval',
      name: '余额后台刷新',
      desc: '只读刷新额度与到期快照，同步最新冻结/解冻状态',
      platforms: ['WorkBuddy'],
      interval: d.balance_refresh_minutes,
      intervalKey: 'balance_refresh_minutes',
      defaultInterval: 5,
      enabled: d.balance_refresh_enabled,
      key: 'balance_refresh',
      statusKey: 'balance',
    },
  ];
});

const GROUPS = [
  { id: 'checkin', title: '签到与联动', desc: '每日签到由「签到设置」页维护，连登管家随签到自动触发' },
  { id: 'hourly', title: '每日整点巡检', desc: '按北京时间整点触发，可展开单独调整每个任务的执行时刻' },
  { id: 'interval', title: '后台周期轮询', desc: '按固定分钟间隔在后台静默运行，停用后不再请求上游' },
];

const filteredGroups = computed(() => GROUPS.map(group => ({
  ...group,
  items: families.value.filter(f => {
    if (f.group !== group.id) return false;
    if (statusFilter.value === 'enabled') return f.enabled;
    if (statusFilter.value === 'disabled') return !f.enabled;
    return true;
  }),
})).filter(group => group.items.length > 0));

const enabledCount = computed(() => families.value.filter(f => f.enabled).length);
const disabledCount = computed(() => families.value.filter(f => !f.enabled).length);
const enabledTotal = computed(() => families.value.length);
const enabledBreakdown = computed(() => {
  const hourly = families.value.filter(f => f.enabled && !f.interval && !f.follows).length;
  const interval = families.value.filter(f => f.enabled && f.interval).length;
  const follows = families.value.filter(f => f.enabled && f.follows).length;
  return { hourly, interval, follows };
});

const dirtyCount = computed(() => {
  const cur = draft.value;
  const base = savedSnapshot.value;
  if (!cur || !base) return 0;
  let count = 0;
  for (const key of ['travel_enabled', 'activity_enabled', 'keepalive_enabled', 'blackcat_enabled', 'growth_enabled', 'streak_enabled', 'task_sweep_enabled', 'balance_refresh_enabled']) {
    if (Boolean(cur[key]) !== Boolean(base[key])) count++;
  }
  for (const key of ['travel_hours', 'activity_hours', 'keepalive_hours', 'blackcat_hours', 'growth_hours']) {
    if (!sameHours(cur[key], base[key])) count++;
  }
  if (Number(cur.task_sweep_minutes) !== Number(base.task_sweep_minutes)) count++;
  if (Number(cur.balance_refresh_minutes) !== Number(base.balance_refresh_minutes)) count++;
  return count;
});
const isDirty = computed(() => dirtyCount.value > 0);

const platformClass = name => `platform-${String(name).toLowerCase()}`;

function toggleExpand(family) {
  if (!family.hoursKey && !family.intervalKey) return;
  expandedId.value = expandedId.value === family.id ? '' : family.id;
}

function toggleFamilyHour(family, hour) {
  if (!draft.value || !family.hoursKey) return;
  const current = draft.value[family.hoursKey] || [];
  if (current.includes(hour)) {
    if (current.length <= 1) {
      Message.info('至少保留 1 个执行时刻');
      return;
    }
    draft.value[family.hoursKey] = current.filter(item => item !== hour);
    return;
  }
  draft.value[family.hoursKey] = normalizeHours([...current, hour], family.defaultHours);
}

function restoreFamilyDefault(family) {
  if (!draft.value) return;
  if (family.hoursKey) draft.value[family.hoursKey] = [...family.defaultHours];
  if (family.intervalKey) draft.value[family.intervalKey] = family.defaultInterval;
}

function resetDraft() {
  if (!savedSnapshot.value) return;
  draft.value = cloneDraft(savedSnapshot.value);
}

function nextFire(family) {
  if (!family.enabled || family.interval || family.follows || !family.plan?.length) return null;
  const now = props.model.now || Date.now();
  return Math.min(...family.plan.map(h => {
    const china = new Date(now + 8 * 3600000);
    const day = Date.UTC(china.getUTCFullYear(), china.getUTCMonth(), china.getUTCDate()) - 8 * 3600000;
    const value = day + h * 3600000;
    return value > now ? value : value + 86400000;
  }));
}

function nextRunText(family) {
  if (!family.enabled) return '已停用';
  if (family.interval) return `每 ${family.interval} 分钟`;
  if (family.follows) return '跟随每次签到';
  const at = nextFire(family);
  if (!at) return '--';
  const now = props.model.now || Date.now();
  const sameDay = new Date(at).toLocaleDateString('zh-CN', { timeZone: 'Asia/Shanghai' }) === new Date(now).toLocaleDateString('zh-CN', { timeZone: 'Asia/Shanghai' });
  return `${sameDay ? '今天' : '明天'} ${new Date(at).toLocaleTimeString('zh-CN', { timeZone: 'Asia/Shanghai', hour: '2-digit', minute: '2-digit', hour12: false })}`;
}

const nextUp = computed(() => {
  const candidates = families.value.map(f => ({ family: f, at: nextFire(f) })).filter(item => item.at);
  if (!candidates.length) return null;
  candidates.sort((a, b) => a.at - b.at);
  const firstAt = candidates[0].at;
  const sameSlot = candidates.filter(item => item.at === firstAt).map(item => item.family.name);
  const now = props.model.now || Date.now();
  const sameDay = new Date(firstAt).toLocaleDateString('zh-CN', { timeZone: 'Asia/Shanghai' }) === new Date(now).toLocaleDateString('zh-CN', { timeZone: 'Asia/Shanghai' });
  return {
    family: candidates[0].family,
    names: sameSlot,
    at: firstAt,
    day: sameDay ? '今天' : '明天',
    time: new Date(firstAt).toLocaleTimeString('zh-CN', { timeZone: 'Asia/Shanghai', hour: '2-digit', minute: '2-digit', hour12: false }),
  };
});

const nextUpCountdown = computed(() => {
  if (!nextUp.value) return '';
  const now = props.model.now || Date.now();
  const minutes = Math.max(1, Math.ceil((nextUp.value.at - now) / 60000));
  return minutes >= 60 ? `${Math.floor(minutes / 60)} 小时 ${minutes % 60} 分` : `${minutes} 分钟`;
});

const currentChinaHour = computed(() => {
  const now = props.model.now || Date.now();
  return new Date(now + 8 * 3600000).getUTCHours();
});

// 24 小时全天排布密度条
const hourDensity = computed(() => {
  const slots = Array.from({ length: 24 }, (_, hour) => ({ hour, tasks: [] }));
  for (const family of families.value) {
    if (!family.enabled || family.follows || family.interval || !family.plan?.length) continue;
    for (const hour of family.plan) {
      if (hour >= 0 && hour <= 23) slots[hour].tasks.push(family.name);
    }
  }
  const max = Math.max(1, ...slots.map(s => s.tasks.length));
  return slots.map(slot => ({
    ...slot,
    ratio: slot.tasks.length ? Math.max(28, Math.round((slot.tasks.length / max) * 100)) : 0,
    isNow: slot.hour === currentChinaHour.value,
    title: slot.tasks.length ? `${formatHour(slot.hour)} · ${slot.tasks.join('、')}` : `${formatHour(slot.hour)} · 无整点任务`,
  }));
});

const todayRuns = computed(() => {
  const logs = props.model.allTodayLogs || [];
  const count = re => logs.filter(entry => re.test(entry.text)).length;
  return {
    checkin: count(/签到|已签/),
    travel: count(/旅行|巡检/),
    background: count(/保活|清扫|余额|keepalive|sweep|usage/),
  };
});

const recentFailures = computed(() => {
  const week = Date.now() - 7 * 86400000;
  return (props.model.allLogs || []).filter(entry => /失败|超时|error|failed|timeout/i.test(entry.text) && new Date(entry.ts).getTime() >= week).length;
});

// 今日时间轴：同一整点多个任务合并分组，避免重复刷屏
const timelineItems = computed(() => {
  const now = props.model.now || Date.now();
  const china = new Date(now + 8 * 3600000);
  const dayStart = Date.UTC(china.getUTCFullYear(), china.getUTCMonth(), china.getUTCDate()) - 8 * 3600000;
  const byHour = new Map();
  for (const family of families.value) {
    if (!family.enabled || family.follows || family.interval || !family.plan?.length) continue;
    for (const hour of family.plan) {
      const bucket = byHour.get(hour) || [];
      bucket.push(family);
      byHour.set(hour, bucket);
    }
  }
  const hours = [...byHour.keys()].sort((a, b) => a - b);
  let nextMarked = false;
  return hours.map(hour => {
    const at = dayStart + hour * 3600000;
    const list = byHour.get(hour);
    const allExecuted = list.every(f => (lastRuns.value[f.statusKey] || 0) * 1000 >= at) && at <= now;
    let status = '待执行';
    let kind = 'pending';
    if (allExecuted) { status = '已执行'; kind = 'done'; }
    else if (at <= now) { status = '已过点'; kind = 'passed'; }
    else if (!nextMarked) { status = '下一次'; kind = 'next'; nextMarked = true; }
    return {
      hour,
      at,
      time: formatHour(hour),
      names: list.map(f => f.name),
      status,
      kind,
    };
  });
});

const activeIntervalSummary = computed(() =>
  families.value.filter(f => f.enabled && f.interval).map(f => `${f.name} ${f.interval}m`).join(' · ') || '无'
);

function lastRunText(family) {
  const seconds = lastRuns.value[family.statusKey] || 0;
  if (!seconds) return '本进程内未执行';
  const date = new Date(seconds * 1000);
  const now = Date.now();
  const sameDay = date.toLocaleDateString('zh-CN', { timeZone: 'Asia/Shanghai' }) === new Date(now).toLocaleDateString('zh-CN', { timeZone: 'Asia/Shanghai' });
  const yesterday = date.toLocaleDateString('zh-CN', { timeZone: 'Asia/Shanghai' }) === new Date(now - 86400000).toLocaleDateString('zh-CN', { timeZone: 'Asia/Shanghai' });
  const time = date.toLocaleTimeString('zh-CN', { timeZone: 'Asia/Shanghai', hour: '2-digit', minute: '2-digit', hour12: false });
  return `${sameDay ? '今天' : yesterday ? '昨天' : date.toLocaleDateString('zh-CN', { timeZone: 'Asia/Shanghai', month: '2-digit', day: '2-digit' })} ${time}`;
}

async function load() {
  loading.value = true;
  try {
    const [data, status] = await Promise.all([
      props.model.api('settings/gateway'),
      props.model.api('schedules/status').catch(() => ({ last_runs: {} })),
    ]);
    config.value = data.config || {};
    lastRuns.value = status.last_runs || {};
    const s = config.value.schedule || {};
    const fresh = cloneDraft({
      checkin_enabled: props.model.schedule?.checkin_enabled ?? s.checkin_enabled ?? true,
      checkin_hours: props.model.schedule?.checkin_hours || s.checkin_hours || [9],
      travel_enabled: !!s.travel_enabled,
      travel_hours: s.travel_hours || [9, 21],
      activity_enabled: !!s.activity_enabled,
      activity_hours: s.activity_hours || [10],
      keepalive_enabled: !!s.keepalive_enabled,
      keepalive_hours: s.keepalive_hours || [22],
      blackcat_enabled: !!s.blackcat_enabled,
      blackcat_hours: s.blackcat_hours || [23],
      growth_enabled: !!s.growth_enabled,
      growth_hours: s.growth_hours || [1],
      streak_enabled: !!s.streak_enabled,
      task_sweep_enabled: !!s.task_sweep_enabled,
      task_sweep_minutes: s.task_sweep_minutes ?? 15,
      balance_refresh_enabled: !!s.balance_refresh_enabled,
      balance_refresh_minutes: s.balance_refresh_minutes ?? 5,
    });
    draft.value = fresh;
    savedSnapshot.value = cloneDraft(fresh);
  } catch (cause) {
    Message.error(cause.message);
  } finally {
    loading.value = false;
  }
}

let pollTimer;
onMounted(() => {
  load();
  pollTimer = setInterval(() => {
    props.model.api('schedules/status').then(data => { lastRuns.value = data.last_runs || {}; }).catch(() => {});
  }, 15000);
});
onBeforeUnmount(() => clearInterval(pollTimer));

watch(() => props.model.schedule, () => {
  if (draft.value && !saving.value) {
    draft.value.checkin_enabled = props.model.schedule?.checkin_enabled ?? draft.value.checkin_enabled;
    draft.value.checkin_hours = normalizeHours(props.model.schedule?.checkin_hours || draft.value.checkin_hours, [9]);
    if (savedSnapshot.value) {
      savedSnapshot.value.checkin_enabled = draft.value.checkin_enabled;
      savedSnapshot.value.checkin_hours = [...draft.value.checkin_hours];
    }
  }
}, { deep: true });

async function save() {
  if (!draft.value) return;
  saving.value = true;
  try {
    const d = cloneDraft(draft.value);
    await props.model.api('settings/gateway', {
      method: 'POST',
      body: JSON.stringify({
        schedule: {
          travel_enabled: d.travel_enabled,
          travel_hours: d.travel_hours,
          activity_enabled: d.activity_enabled,
          activity_hours: d.activity_hours,
          keepalive_enabled: d.keepalive_enabled,
          keepalive_hours: d.keepalive_hours,
          blackcat_enabled: d.blackcat_enabled,
          blackcat_hours: d.blackcat_hours,
          growth_enabled: d.growth_enabled,
          growth_hours: d.growth_hours,
          streak_enabled: d.streak_enabled,
          task_sweep_enabled: d.task_sweep_enabled,
          task_sweep_minutes: d.task_sweep_minutes,
          balance_refresh_enabled: d.balance_refresh_enabled,
          balance_refresh_minutes: d.balance_refresh_minutes,
        },
      }),
    });
    draft.value = d;
    savedSnapshot.value = cloneDraft(d);
    savedAt.value = Date.now();
    pauseConfirm.value = false;
    Message.success('定时任务已保存（启停与计划即时热生效）');
  } catch (cause) {
    Message.error(cause.message);
  } finally {
    saving.value = false;
  }
}

function pauseAll() {
  if (!draft.value) return;
  for (const family of families.value) {
    if (family.key) draft.value[family.key + '_enabled'] = false;
  }
  save();
}
</script>

<template>
  <section class="schedules-view" aria-label="定时任务">
    <header class="page-heading sched-heading">
      <div class="heading-meta">
        <span v-if="isDirty" class="dirty-pill" role="status">
          <i class="status-dot" />有 {{ dirtyCount }} 项未保存更改
        </span>
        <span v-else class="saved-meta">
          <icon-check />{{ savedAt ? `上次保存 ${clockTime(savedAt).slice(0, 5)}` : '配置已同步 · 启停与计划热生效' }}
        </span>
      </div>
      <div class="sched-actions">
        <a-button v-if="embedded" @click="emit('key')"><template #icon><icon-safe /></template>管理密钥</a-button>
        <a-button :loading="loading" @click="load"><template #icon><icon-refresh /></template>刷新</a-button>
        <a-button :disabled="!draft || !families.some(f => f.enabled && f.key)" @click="pauseConfirm = true">
          <template #icon><icon-pause-circle /></template>全部暂停
        </a-button>
        <a-button type="primary" :loading="saving" :disabled="!draft || !isDirty" @click="save">
          <template #icon><icon-check /></template>保存设置
        </a-button>
      </div>
    </header>

    <section class="stat-strip sched-strip" aria-label="调度概览">
      <div class="stat-seg">
        <span>已启用任务</span>
        <strong :class="enabledCount ? 'accent-text' : ''">{{ enabledCount }} / {{ enabledTotal }}</strong>
        <small>整点 {{ enabledBreakdown.hourly }} · 轮询 {{ enabledBreakdown.interval }} · 跟随 {{ enabledBreakdown.follows }}</small>
      </div>
      <i class="stat-divider" />
      <div class="stat-seg">
        <span>下次触发</span>
        <strong>{{ nextUp ? nextUp.time : '--:--' }}</strong>
        <small>{{ nextUp ? `${nextUp.names.join('、')} · 还有 ${nextUpCountdown}` : '所有整点任务已停用' }}</small>
      </div>
      <i class="stat-divider" />
      <div class="stat-seg">
        <span>今日执行</span>
        <strong>{{ todayRuns.checkin + todayRuns.travel + todayRuns.background }}</strong>
        <small>签到 {{ todayRuns.checkin }} · 巡检 {{ todayRuns.travel }} · 后台 {{ todayRuns.background }}</small>
      </div>
      <i class="stat-divider" />
      <div class="stat-seg">
        <span>近 7 天异常</span>
        <strong :class="recentFailures ? 'warning-text' : ''">{{ recentFailures }}</strong>
        <small>运行日志中失败或超时条目</small>
      </div>
    </section>

    <div class="sched-layout">
      <section class="panel-card sched-board">
        <header class="board-head">
          <div class="filter-tabs" role="group" aria-label="任务状态筛选">
            <button type="button" :class="{ active: statusFilter === 'all' }" @click="statusFilter = 'all'">
              全部 <span>{{ enabledTotal }}</span>
            </button>
            <button type="button" :class="{ active: statusFilter === 'enabled' }" @click="statusFilter = 'enabled'">
              运行中 <span>{{ enabledCount }}</span>
            </button>
            <button type="button" :class="{ active: statusFilter === 'disabled' }" @click="statusFilter = 'disabled'">
              已停用 <span>{{ disabledCount }}</span>
            </button>
          </div>
          <a-button size="small" type="text" @click="emit('navigate', 'logs')">
            运行日志<icon-arrow-right />
          </a-button>
        </header>

        <div v-if="filteredGroups.length" class="group-stack">
          <section v-for="group in filteredGroups" :key="group.id" class="task-group">
            <header class="group-head">
              <h2>{{ group.title }}</h2>
              <small>{{ group.desc }}</small>
            </header>

            <div class="task-list">
              <article
                v-for="family in group.items"
                :key="family.id"
                :class="['task-card', { off: !family.enabled, expanded: expandedId === family.id }]"
              >
                <div class="task-card-main">
                  <div class="task-info">
                    <div class="task-title-line">
                      <strong>{{ family.name }}</strong>
                      <span
                        v-for="p in family.platforms"
                        :key="p"
                        class="task-platform"
                        :class="[platformClass(p), { multi: family.platforms.length > 1 }]"
                      >
                        {{ p }}
                      </span>
                      <a-tag size="small" :color="family.enabled ? 'arcoblue' : 'gray'">
                        {{ family.enabled ? '运行中' : '已停用' }}
                      </a-tag>
                    </div>
                    <p class="task-desc">{{ family.desc }}</p>
                    <div class="task-meta-line">
                      <div class="schedule-chips">
                        <template v-if="family.interval">
                          <span class="schedule-chip"><icon-clock-circle />每 {{ family.interval }} 分钟</span>
                        </template>
                        <template v-else-if="family.plan?.length">
                          <span v-for="hour in family.plan" :key="hour" class="schedule-chip">{{ formatHour(hour) }}</span>
                        </template>
                        <template v-else-if="family.follows">
                          <span class="schedule-chip">跟随每次签到</span>
                        </template>
                      </div>
                      <span class="run-meta">上次 {{ lastRunText(family) }}</span>
                      <span class="run-meta">下次 {{ nextRunText(family) }}</span>
                    </div>
                  </div>

                  <div class="task-controls">
                    <a-button
                      v-if="family.readonly"
                      size="mini"
                      type="text"
                      @click="emit('navigate', 'settings')"
                    >
                      去设置<icon-arrow-right />
                    </a-button>
                    <a-button
                      v-else-if="family.hoursKey || family.intervalKey"
                      size="mini"
                      type="secondary"
                      @click="toggleExpand(family)"
                    >
                      <template #icon><icon-up v-if="expandedId === family.id" /><icon-edit v-else /></template>
                      {{ expandedId === family.id ? '收起' : '调计划' }}
                    </a-button>

                    <a-switch
                      v-if="family.readonly"
                      :model-value="family.enabled"
                      size="small"
                      disabled
                      aria-label="在签到设置页维护"
                    />
                    <a-switch
                      v-else-if="draft"
                      v-model="draft[family.key + '_enabled']"
                      size="small"
                      :aria-label="'启用' + family.name"
                    />
                  </div>
                </div>

                <div v-if="expandedId === family.id && family.hoursKey" class="inline-editor">
                  <div class="editor-head">
                    <span>选择执行整点（北京时间 · 已选 {{ family.plan.length }} 个）</span>
                    <button type="button" class="reset-default-btn" @click="restoreFamilyDefault(family)">
                      恢复默认（{{ family.defaultHours.map(formatHour).join(' · ') }}）
                    </button>
                  </div>
                  <div class="hour-matrix" role="group" :aria-label="family.name + '整点选择'">
                    <button
                      v-for="hour in HOURS"
                      :key="hour"
                      type="button"
                      :class="['hour-cell', { selected: family.plan.includes(hour) }]"
                      :aria-pressed="family.plan.includes(hour)"
                      @click="toggleFamilyHour(family, hour)"
                    >
                      {{ formatHour(hour) }}
                    </button>
                  </div>
                  <p v-if="family.hint" class="editor-note"><icon-info-circle />{{ family.hint }}</p>
                </div>

                <div v-else-if="expandedId === family.id && family.intervalKey && draft" class="inline-editor">
                  <div class="editor-head">
                    <span>轮询间隔（分钟）</span>
                    <button type="button" class="reset-default-btn" @click="restoreFamilyDefault(family)">
                      恢复默认（{{ family.defaultInterval }} 分钟）
                    </button>
                  </div>
                  <div class="interval-editor-row">
                    <div class="interval-presets">
                      <button
                        v-for="mins in INTERVAL_PRESETS"
                        :key="mins"
                        type="button"
                        :class="['preset-pill', { active: Number(draft[family.intervalKey]) === mins }]"
                        @click="draft[family.intervalKey] = mins"
                      >
                        {{ mins }} 分钟
                      </button>
                    </div>
                    <div class="interval-custom">
                      <span>自定义</span>
                      <a-input-number
                        v-model="draft[family.intervalKey]"
                        size="small"
                        :min="1"
                        :max="1440"
                        :step="1"
                        style="width: 96px"
                      />
                      <span>分钟</span>
                    </div>
                  </div>
                </div>
              </article>
            </div>
          </section>
        </div>
        <a-empty v-else-if="!loading" :description="families.length ? '当前筛选下暂无任务' : '定时任务配置读取失败，请刷新重试'" />

        <footer class="board-footer">
          <span :class="['footer-status', { dirty: isDirty }]">
            <template v-if="isDirty">有 {{ dirtyCount }} 项未保存修改（保存后即时热生效）</template>
            <template v-else><icon-check />当前排程与运行中配置一致</template>
          </span>
          <div class="footer-actions">
            <a-button type="text" size="small" :disabled="!isDirty || saving" @click="resetDraft">
              <template #icon><icon-undo /></template>还原
            </a-button>
            <a-button type="primary" size="small" :loading="saving" :disabled="!draft || !isDirty" @click="save">
              <template #icon><icon-check /></template>保存设置
            </a-button>
          </div>
        </footer>
      </section>

      <aside class="sched-side">
        <section class="command-card" aria-label="下次自动触发与全天排布">
          <div class="inline-between">
            <span class="command-caption">下次自动触发</span>
            <span :class="['side-badge', { off: !enabledCount }]">
              {{ enabledCount ? `${enabledCount} 项运行中` : '全部已停用' }}
            </span>
          </div>

          <div class="command-clock">
            <strong>{{ nextUp ? nextUp.time : '--:--' }}</strong>
            <span v-if="nextUp">{{ nextUp.day }}</span>
          </div>
          <p class="command-date">
            {{ nextUp ? `${nextUp.names.join('、')} · 还有 ${nextUpCountdown}` : '当前没有启用的整点任务' }}
          </p>

          <div class="density-block">
            <div class="inline-between density-head">
              <span>今日 24 小时整点排布</span>
              <small>现在 {{ formatHour(currentChinaHour) }}</small>
            </div>
            <div class="density-bars" role="img" aria-label="24 小时整点任务分布">
              <div
                v-for="slot in hourDensity"
                :key="slot.hour"
                :class="['density-col', { now: slot.isNow, active: slot.tasks.length > 0 }]"
                :title="slot.title"
              >
                <span class="density-bar-wrap">
                  <i :style="{ height: slot.ratio + '%' }" />
                </span>
              </div>
            </div>
            <div class="density-axis">
              <span>00</span><span>06</span><span>12</span><span>18</span><span>23</span>
            </div>
          </div>

          <dl class="command-meta">
            <div>
              <dt>后台轮询</dt>
              <dd>{{ activeIntervalSummary }}</dd>
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

        <section class="panel-card timeline-card" aria-label="今日执行时间轴">
          <header class="ledger-head">
            <div class="ledger-title">
              <h2>今日时间轴</h2>
              <small>按整点合并展示 · 仅含已启用任务</small>
            </div>
          </header>
          <div v-if="timelineItems.length" class="timeline" role="list">
            <div v-for="(item, index) in timelineItems" :key="item.hour" class="timeline-item" role="listitem">
              <time>{{ item.time }}</time>
              <span class="timeline-track">
                <i :class="['timeline-dot', { done: item.kind === 'done', next: item.kind === 'next' }]" />
                <i v-if="index < timelineItems.length - 1" class="timeline-line" />
              </span>
              <span class="timeline-info">
                <strong>{{ item.names.join('、') }}</strong>
                <small :class="{ 'accent-text': item.kind === 'next' }">{{ item.status }}</small>
              </span>
            </div>
          </div>
          <a-empty v-else description="当前没有启用的整点任务" />
        </section>

        <section class="panel-card notes-card">
          <header class="ledger-head"><div class="ledger-title"><h2>调度说明</h2></div></header>
          <p class="billing-hint-inline">
            每日签到的开关与时刻归属
            <button type="button" class="inline-link" @click="emit('navigate', 'settings')">签到设置</button>
            页维护，连登管家会在每次签到后自动跟随执行。
          </p>
          <p class="billing-hint-inline">
            未启用的任务族不会对上游发起任何请求；覆盖已停用账号等高级选项位于
            <button v-if="model.capabilities?.gateway" type="button" class="inline-link" @click="emit('navigate', 'gateway')">网关设置</button>
            <span v-else>网关设置</span>。
          </p>
        </section>
      </aside>
    </div>

    <a-modal :visible="pauseConfirm" title="全部暂停定时任务" width="440px" :footer="false" :mask-closable="false" @cancel="pauseConfirm = false">
      <p class="modal-description">将停用旅行巡检、活跃上报、Token 保活、夜猫子、成长任务、连登管家、任务补领清扫与余额后台刷新。</p>
      <p class="remove-notice">每日签到不受影响（在「签到设置」页维护）；停用后各任务族不再对上游发起任何请求，可随时在本页重新开启。</p>
      <div class="modal-actions">
        <a-button @click="pauseConfirm = false">取消</a-button>
        <a-button type="primary" status="danger" :loading="saving" @click="pauseAll">确认全部暂停</a-button>
      </div>
    </a-modal>
  </section>
</template>

<style scoped>
.schedules-view {
  display: grid;
  gap: 14px;
}

.sched-heading {
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

.sched-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-left: auto;
}

.sched-strip strong {
  font-variant-numeric: tabular-nums;
}

.sched-layout {
  display: grid;
  grid-template-columns: minmax(0, 1.72fr) minmax(300px, 1fr);
  gap: 14px;
  align-items: start;
}

.sched-board {
  padding: 0;
  gap: 0;
  overflow: hidden;
}

.board-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  padding: 14px 20px;
  border-bottom: 1px solid var(--line);
}

.group-stack {
  display: grid;
}

.task-group {
  border-bottom: 1px solid var(--line);
}

.task-group:last-child {
  border-bottom: 0;
}

.group-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  padding: 12px 20px 8px;
  background: var(--surface-secondary);
  border-bottom: 1px solid var(--line);
}

.group-head h2 {
  margin: 0;
  font-size: 12.5px;
  font-weight: 600;
}

.group-head small {
  color: var(--muted);
  font-size: 11px;
}

.task-list {
  display: grid;
}

.task-card {
  border-bottom: 1px solid var(--line);
  transition: background-color 0.14s ease;
}

.task-card:last-child {
  border-bottom: 0;
}

.task-card.expanded {
  background: var(--surface-secondary);
}

.task-card-main {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  padding: 14px 20px;
}

.task-card.off .task-title-line strong {
  color: var(--secondary);
}

.task-info {
  flex: 1;
  min-width: 0;
  display: grid;
  gap: 5px;
}

.task-title-line {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
}

.task-title-line strong {
  font-size: 13.5px;
  font-weight: 600;
}

.task-desc {
  color: var(--muted);
  font-size: 11.5px;
  line-height: 1.5;
}

.task-meta-line {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px 12px;
  margin-top: 2px;
}

.task-meta-line .schedule-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-variant-numeric: tabular-nums;
}

.run-meta {
  font-size: 11px;
  color: var(--muted);
  font-variant-numeric: tabular-nums;
}

.task-controls {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-shrink: 0;
  padding-top: 2px;
}

.inline-editor {
  padding: 0 20px 16px;
  display: grid;
  gap: 10px;
}

.editor-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  flex-wrap: wrap;
  font-size: 11.5px;
  color: var(--secondary);
}

.reset-default-btn {
  border: 0;
  background: transparent;
  color: var(--accent);
  font-size: 11px;
  padding: 2px 4px;
}

.reset-default-btn:hover {
  text-decoration: underline;
  text-underline-offset: 2px;
}

.hour-matrix {
  display: grid;
  grid-template-columns: repeat(8, minmax(0, 1fr));
  gap: 6px;
}

.hour-cell {
  height: 32px;
  border: 1px solid var(--line);
  border-radius: 7px;
  background: var(--surface);
  color: var(--secondary);
  font-family: Inter, 'JetBrains Mono', Consolas, monospace;
  font-size: 11px;
  font-variant-numeric: tabular-nums;
  transition: border-color 0.12s ease, background-color 0.12s ease, color 0.12s ease;
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

.editor-note {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 11px;
  color: var(--muted);
}

.interval-editor-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.interval-presets {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.preset-pill {
  border: 1px solid var(--line);
  background: var(--surface);
  color: var(--secondary);
  border-radius: 7px;
  padding: 5px 10px;
  font-size: 11.5px;
  font-variant-numeric: tabular-nums;
}

.preset-pill:hover {
  border-color: var(--accent);
  color: var(--ink);
}

.preset-pill.active {
  border-color: var(--accent);
  background: var(--accent-soft);
  color: var(--accent);
  font-weight: 500;
}

.interval-custom {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 11.5px;
  color: var(--muted);
}

.board-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  padding: 12px 20px;
  border-top: 1px solid var(--line);
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
  margin-left: auto;
}

.sched-side {
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
  font-size: 11.5px;
}

.density-block {
  margin-top: 2px;
  padding: 10px 12px;
  border-radius: 10px;
  background: #1d1f26;
  border: 1px solid #2b2d35;
  display: grid;
  gap: 6px;
}

.density-head {
  font-size: 10.5px;
  color: #979aa4;
}

.density-head small {
  font-family: Inter, Consolas, monospace;
  font-size: 10px;
  color: #b8c3ff;
}

.density-bars {
  display: grid;
  grid-template-columns: repeat(24, minmax(0, 1fr));
  gap: 3px;
  height: 34px;
  align-items: end;
}

.density-col {
  height: 100%;
  display: flex;
  align-items: flex-end;
  cursor: default;
}

.density-bar-wrap {
  width: 100%;
  height: 100%;
  display: flex;
  align-items: flex-end;
  background: #262933;
  border-radius: 3px;
  overflow: hidden;
}

.density-col.now .density-bar-wrap {
  box-shadow: inset 0 0 0 1px #91a6ff;
}

.density-bar-wrap i {
  display: block;
  width: 100%;
  background: #5b74e8;
  border-radius: 2px 2px 0 0;
  transition: height 0.2s ease;
}

.density-col.now .density-bar-wrap i {
  background: #a6b7ff;
}

.density-axis {
  display: flex;
  justify-content: space-between;
  font-family: Inter, Consolas, monospace;
  font-size: 9.5px;
  color: #777a85;
}

.command-meta {
  margin: 2px 0 0;
  padding-top: 8px;
  border-top: 1px solid #2b2d35;
  display: grid;
}

.command-meta > div {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 10px;
  padding: 6px 0;
  font-size: 11px;
}

.command-meta dt {
  color: #979aa4;
  flex-shrink: 0;
}

.command-meta dd {
  margin: 0;
  text-align: right;
  color: #e4e5ea;
  font-variant-numeric: tabular-nums;
  overflow-wrap: anywhere;
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

.timeline-card .ledger-head,
.notes-card .ledger-head {
  margin-bottom: 0;
}

.notes-card .billing-hint-inline:last-child {
  margin-bottom: 0;
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

@media (max-width: 1180px) {
  .sched-layout {
    grid-template-columns: minmax(0, 1fr);
  }

  .sched-side {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    align-items: start;
  }
}

@media (max-width: 820px) {
  .sched-actions {
    width: 100%;
    margin-left: 0;
  }

  .sched-actions .arco-btn:last-child {
    margin-left: auto;
  }

  .sched-strip {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 10px 0;
    padding: 12px 4px;
  }

  .sched-strip .stat-divider {
    display: none;
  }

  .sched-strip .stat-seg {
    padding: 2px 14px;
    border-left: 1px solid var(--line);
  }

  .sched-strip .stat-seg:nth-of-type(odd) {
    border-left: 0;
  }

  .sched-side {
    grid-template-columns: minmax(0, 1fr);
  }

  .board-head,
  .group-head,
  .task-card-main,
  .inline-editor,
  .board-footer {
    padding-inline: 16px;
  }

  .task-card-main {
    flex-direction: column;
    gap: 10px;
  }

  .task-controls {
    width: 100%;
    justify-content: flex-end;
  }

  .hour-matrix {
    grid-template-columns: repeat(6, minmax(0, 1fr));
  }
}

@media (max-width: 480px) {
  .hour-matrix {
    grid-template-columns: repeat(4, minmax(0, 1fr));
  }
}
</style>
