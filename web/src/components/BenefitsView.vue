<script setup>
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { Alert as AAlert, Button as AButton, Empty as AEmpty, Input as AInput, Modal as AModal, Option as AOption, Select as ASelect, Spin as ASpin, Tag as ATag, Message } from '@arco-design/web-vue';
import { IconRefresh, IconPlayArrow, IconSearch, IconGift, IconCopy, IconCheck, IconClockCircle, IconArrowRight, IconThunderbolt } from '@arco-design/web-vue/es/icon';
import { formatNumber } from '../useAccounts';

const props = defineProps({ model: Object, section: String });
const uid = ref(''), loading = ref(false), working = ref(false), error = ref(''), result = ref('');
const tasks = ref([]), automatic = ref([]), rewards = ref(null);
const queue = ref({ running: false, items: [] }), scan = ref(null), concurrency = ref(1);
const filter = ref('all'), search = ref(''), confirmation = ref(null);
const loadedAt = ref(null);
let version = 0, queueTimer;
const abort = new AbortController();
const api = (path, options = {}) => props.model.api(path, { ...options, signal: abort.signal });
const supported = computed(() => props.model.provider === 'workbuddy' && props.model.platformSelection === 'workbuddy-cn');
const personal = computed(() => supported.value ? props.model.accounts.filter(a => !a.disabled && !a.enterprise && a.benefits_supported !== false) : []);
const unsupportedAccounts = computed(() => supported.value ? props.model.accounts.filter(a => a.benefits_supported === false) : []);
const options = personal;
const account = computed(() => options.value.find(a => a.uid === uid.value));
const workbuddyReady = computed(() => supported.value && props.model.accounts.length > 0);
const completedTasks = computed(() => tasks.value.filter(t => t.claimed).length);
const claimableTasks = computed(() => tasks.value.filter(t => t.claimable));
const pendingTasksCount = computed(() => tasks.value.filter(t => !t.claimed && !t.locked).length);
const runningQueueCount = computed(() => (queue.value.items || []).filter(i => i.status === 'running').length);
const claimableCredits = computed(() => claimableTasks.value.reduce((sum, t) => sum + Number(t.credit || 0), 0));
const taskProgress = computed(() => tasks.value.length ? completedTasks.value / tasks.value.length : 0);
const taskProgressPercent = computed(() => Math.round(taskProgress.value * 100));
const taskCounts = computed(() => ({
  all: tasks.value.length,
  pending: tasks.value.filter(t => !t.claimed && !t.claimable).length,
  claimable: claimableTasks.value.length,
  claimed: completedTasks.value,
  error: tasks.value.filter(t => queue.value.items?.some(i => i.uid === uid.value && i.code === t.task_code && i.status === 'error')).length,
}));
const queueProgressPercent = computed(() => queue.value.total ? Math.min(100, Math.round((done.value / queue.value.total) * 100)) : 0);
function taskProgressRatio(task) {
  const target = Number(task?.target || 0);
  if (target <= 0) return task?.claimed ? 100 : 0;
  return Math.max(0, Math.min(100, Math.round((Number(task.current || 0) / target) * 100)));
}
function isPersonalAccount(item) {
  return personal.value.some(a => a.uid === item.uid);
}
async function claimAllTasks() {
  if (working.value || queue.value.running || !account.value || !claimableTasks.value.length) return;
  const selected = uid.value, platform = props.model.platformSelection;
  const list = [...claimableTasks.value];
  working.value = true; error.value = ''; result.value = '';
  let claimed = 0;
  const failures = [];
  try {
    for (const task of list) {
      try {
        await api(`accounts/${encodeURIComponent(selected)}/tasks/claim`, { method: 'POST', body: JSON.stringify({ task_code: task.task_code }) });
        claimed++;
      } catch (cause) {
        if (cause.name === 'AbortError') return;
        failures.push(`${task.title || task.task_code}：${cause.message}`);
      }
    }
    try { await api(`accounts/${encodeURIComponent(selected)}/balance`, { method: 'POST' }); } catch { /* ignore balance error */ }
    await props.model.refreshOverview();
    if (uid.value === selected && props.section === 'tasks' && props.model.platformSelection === platform) {
      await load();
      result.value = `已领取 ${claimed} 项任务奖励`;
      if (failures.length) error.value = failures.join('；');
    }
    if (failures.length) Message.warning(`已领取 ${claimed} 项，${failures.length} 项失败`);
    else Message.success(`已领取 ${claimed} 项任务奖励`);
  } catch (cause) {
    if (cause.name !== 'AbortError') error.value = cause.message;
  } finally {
    working.value = false;
  }
}
const availableTiers = computed(() => (rewards.value?.streak?.redemption_status?.tiers || []).filter(canRedeem));
const streakTiers = computed(() => rewards.value?.streak?.redemption_status?.tiers || []);
const streakDays = computed(() => Number(rewards.value?.streak?.streak?.days ?? 0));
const streakHasData = computed(() => rewards.value?.streak?.streak?.days !== undefined && rewards.value?.streak?.streak?.days !== null);
const makeupCards = computed(() => rewards.value?.streak?.makeup_cards?.balance);
const maxTierDays = computed(() => streakTiers.value.reduce((max, tier) => Math.max(max, Number(tier.days || 0)), 0) || 28);
const streakProgress = computed(() => streakHasData.value && maxTierDays.value > 0 ? Math.min(100, Math.round((streakDays.value / maxTierDays.value) * 100)) : 0);
const canCompensate = computed(() => rewards.value?.compensation?.active && !rewards.value.compensation.claimed && rewards.value.compensation.credit_num > 0);
const claimCount = computed(() => availableTiers.value.length + Number(!!canCompensate.value));
const rewardCredits = computed(() => availableTiers.value.reduce((sum, t) => sum + Number(t.credit || 0), 0) + (canCompensate.value ? Number(rewards.value.compensation.credit_num) : 0));
const gatewayBatchActions = [
  { path: 'compensation_all', label: '全账号补偿', desc: '为全部可用账号领取官方补偿积分' },
  { path: 'travel_all', label: '旅行巡检', desc: '立即执行一次猫猫旅行派出与领奖' },
  { path: 'activity_all', label: '活跃上报', desc: '触发全账号每日活跃与连登点亮' },
  { path: 'keepalive_all', label: 'Token 保活', desc: '立即刷新全账号登录凭证状态' },
];
function tierState(tier) {
  const raw = tierStatus(tier);
  if (raw === 'claimed' || raw === 'redeemed') return { kind: 'claimed', label: '已领取', color: 'arcoblue' };
  if (canRedeem(tier)) return { kind: 'claimable', label: '可兑换', color: 'orange' };
  return { kind: 'locked', label: statusName(raw || 'locked'), color: 'gray' };
}
function tierRemainingDays(tier) {
  if (!streakHasData.value || !tier?.days) return 0;
  return Math.max(0, Number(tier.days) - streakDays.value);
}
function packageRatio(remain, size) {
  const total = Number(size || 0);
  if (total <= 0) return 0;
  return Math.max(0, Math.min(100, Math.round((Number(remain || 0) / total) * 100)));
}
const scanAccounts = computed(() => {
  const uids = new Set(props.model.accounts.map(a => a.uid));
  return (scan.value?.accounts || []).filter(a => uids.has(a.uid));
});
const scanPending = computed(() => scanAccounts.value.reduce((sum, a) => sum + (a.growth || []).length, 0));
const coverage = computed(() => props.model.accounts.map(a => ({ ...a, pendingTasks: scanAccounts.value.find(item => item.uid === a.uid) })));
const taskRows = computed(() => tasks.value.filter(t => (!search.value || (t.title + ' ' + t.task_code).toLowerCase().includes(search.value.toLowerCase())) && (filter.value === 'all' || filter.value === 'claimable' && t.claimable || filter.value === 'claimed' && t.claimed || filter.value === 'pending' && !t.claimed && !t.claimable || filter.value === 'error' && queue.value.items?.some(i => i.uid === uid.value && i.code === t.task_code && i.status === 'error'))));
const done = computed(() => (queue.value.items || []).filter(i => ['done', 'skipped', 'error'].includes(i.status)).length);
const statusName = value => ({ pending: '等待执行', running: '执行中', done: '已完成', skipped: '已跳过', error: '失败', locked: '未解锁', claimed: '已领取', redeemed: '已兑换', available: '可兑换', unlocked: '可兑换', claimable: '可兑换' })[value] || value || '--';
const date = value => {
  if (!value) return '--';
  const instant = typeof value === 'string' && /^\d{4}-\d\d-\d\d /.test(value) ? value.replace(' ', 'T') + '+08:00' : value;
  const parsed = new Date(instant);
  return Number.isNaN(parsed.getTime()) ? String(value) : parsed.toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai', hour12: false });
};
function tierStatus(tier) { return rewards.value?.streak?.redemption_status?.['tier_' + tier.tier + '_status']; }
function canRedeem(tier) { return tier.days > 0 && rewards.value?.streak?.streak?.days >= tier.days && !!tierStatus(tier) && !['locked', 'claimed', 'redeemed'].includes(tierStatus(tier)); }
function taskState(task) {
  if (task.claimed) return { label: '已完成', color: 'arcoblue' };
  const queued = queue.value.items?.find(item => item.uid === uid.value && item.code === task.task_code);
  if (queued?.status === 'running') return { label: '执行中', color: 'arcoblue' };
  if (queued?.status === 'error') return { label: '失败', color: 'red' };
  return { label: task.locked ? '未解锁' : task.claimable ? '待领取' : '待执行', color: task.claimable ? 'orange' : 'gray' };
}

async function load() {
  const request = ++version;
  const section = props.section, selected = uid.value;
  error.value = ''; result.value = ''; loading.value = true;
  tasks.value = []; automatic.value = []; rewards.value = null;
  try {
    if (selected) {
      const response = await api(section === 'tasks' ? `accounts/${encodeURIComponent(selected)}/tasks` : `benefits/accounts/${encodeURIComponent(selected)}/rewards`);
      if (request !== version) return;
      if (section === 'tasks') { tasks.value = response.tasks || []; automatic.value = response.automatable_codes || []; }
      else rewards.value = response;
    }
  } catch (cause) { if (request === version && cause.name !== 'AbortError') error.value = cause.message; }
  finally { if (request === version) { loading.value = false; loadedAt.value = new Date(); } }
}
async function refresh() {
  if (working.value || loading.value) return;
  await load();
}
watch([() => props.section, () => props.model.platformSelection], () => {
  uid.value = options.value[0]?.uid || '';
  scan.value = null; filter.value = 'all'; search.value = ''; confirmation.value = null;
  load();
}, { immediate: true });
watch(() => options.value.map(a => a.uid).join(','), () => {
  if (!account.value) { uid.value = options.value[0]?.uid || ''; load(); }
});
function selectAccount(value) { uid.value = value || ''; load(); }
async function pollQueue() {
  clearTimeout(queueTimer);
  if (!supported.value) return;
  try {
    const previous = queue.value.running;
    const response = await api('tasks/queue');
    if (!supported.value) return;
    queue.value = response;
    if (previous && !queue.value.running) { await props.model.refreshOverview(); if (props.section === 'tasks') await load(); }
    if (queue.value.running) queueTimer = setTimeout(pollQueue, 2000);
  } catch (cause) { if (supported.value && cause.name !== 'AbortError') { error.value = cause.message; queueTimer = setTimeout(pollQueue, 5000); } }
}
watch(supported, value => {
  clearTimeout(queueTimer);
  queue.value = { running: false, items: [] };
  if (value) pollQueue();
}, { immediate: true });
onBeforeUnmount(() => { ++version; clearTimeout(queueTimer); abort.abort(); });
async function scanTasks() {
  if (working.value || !supported.value || !personal.value.length) return;
  const platform = props.model.platformSelection;
  working.value = true;
  try {
    const response = await api('tasks/scan_all', { method: 'POST' });
    if (platform === props.model.platformSelection) scan.value = response;
  }
  catch (cause) { if (platform === props.model.platformSelection && cause.name !== 'AbortError') error.value = cause.message; }
  finally { working.value = false; }
}
async function execute(operation, label, payload = {}) {
  if (working.value || queue.value.running || !supported.value || (operation === 'queue' ? !personal.value.length : !account.value)) return;
  const selected = uid.value, section = props.section, platform = props.model.platformSelection;
  working.value = true; error.value = ''; confirmation.value = null;
  try {
    const response = operation === 'queue'
      ? await api('tasks/run_queue', { method: 'POST', body: JSON.stringify({ growth: true, concurrency: concurrency.value }) })
      : await api(section === 'tasks' ? `accounts/${encodeURIComponent(selected)}/tasks/${operation}` : `benefits/accounts/${encodeURIComponent(selected)}/rewards`, { method: 'POST', body: JSON.stringify(payload) });
    if (operation === 'queue') {
      await pollQueue();
      if (section === props.section && platform === props.model.platformSelection) result.value = response.message || '任务队列已启动';
      return;
    }
    if (section === 'tasks' && operation !== 'accept') {
      try { await api(`accounts/${encodeURIComponent(selected)}/balance`, { method: 'POST' }); }
      catch { Message.warning('操作已返回，但余额刷新失败，请稍后刷新积分'); }
    }
    await props.model.refreshOverview();
    if (section === props.section && selected === uid.value && platform === props.model.platformSelection) {
      await load();
      result.value = response.claim_error || response.claimable === false && operation === 'auto' && !response.claimed
        ? response.claim_error || response.message || '任务尚未确认完成，请稍后刷新'
        : response.message || (response.prize ? JSON.stringify(response.prize) : label + '成功');
    }
    if (response.claim_error || response.claimable === false && operation === 'auto' && !response.claimed) Message.warning(response.message || '任务尚未确认完成');
    else Message.success(response.message || label + '成功');
  } catch (cause) { if (section === props.section && platform === props.model.platformSelection && cause.name !== 'AbortError') error.value = cause.message; }
  finally { working.value = false; }
}
function confirmTask(task) { confirmation.value = task ? { title: '执行任务', task } : { title: '执行全部待办' }; }
function confirmExecution() { if (confirmation.value?.rewards) return claimRewards(); const task = confirmation.value?.task; execute(task ? 'auto' : 'queue', '执行', task ? { task_code: task.task_code } : {}); }
async function claimRewards() {
  if (working.value || queue.value.running || !account.value || !claimCount.value) return;
  const selected = uid.value, provider = props.model.provider;
  const actions = [...availableTiers.value.map(t => ({ action: 'redeem', tier: t.tier })), ...(canCompensate.value ? [{ action: 'compensation' }] : [])];
  working.value = true; confirmation.value = null; error.value = '';
  let claimed = 0;
  const failures = [];
  try {
    for (const action of actions) {
      try { await api(`benefits/accounts/${encodeURIComponent(selected)}/rewards`, { method: 'POST', body: JSON.stringify(action) }); claimed++; }
      catch (cause) { if (cause.name === 'AbortError') return; failures.push(cause.message); }
    }
    await props.model.refreshOverview();
    if (uid.value === selected && props.section === 'rewards' && props.model.provider === provider) {
      await load(); result.value = `已领取 ${claimed} 项奖励`; error.value = failures.join('；');
    }
  } catch (cause) { if (cause.name !== 'AbortError') error.value = cause.message; }
  finally { working.value = false; }
}
async function copy(value) { try { await navigator.clipboard.writeText(value); Message.success('已复制券码'); } catch { Message.error('复制失败'); } }
// ── 网关能力补齐（经典控制台迁移）─────────────────────────────
const gateway = computed(() => !!props.model.capabilities?.gateway);
const gatewayWorking = ref('');
async function gatewayBatch(path, label) {
  if (gatewayWorking.value) return;
  gatewayWorking.value = path;
  error.value = ''; result.value = '';
  try {
    const data = await api(path, { method: 'POST' });
    if (path === 'compensation_all' && data.summary) {
      const s = data.summary;
      result.value = `全账号补偿领取完成：合计 ${s.total} · 领取 ${s.claimed}（+${formatNumber(s.credit)} 分）· 跳过 ${s.skipped} · 失败 ${s.errored}`;
      if (!s.ok && s.ok === false) Message.warning('部分账号未拿到确定结论，请看结果详情');
      else Message.success('补偿领取完成');
    } else Message.success(label + '已触发，进度看运行日志');
    await props.model.refreshOverview();
  } catch (cause) { if (cause.name !== 'AbortError') error.value = cause.message; }
  finally { gatewayWorking.value = ''; }
}
const packages = ref(null), packagesLoading = ref(false), expandedPackage = ref('');
const packageRows = computed(() => (packages.value?.accounts || []).filter(a => a.uid).map(account => {
  let earliest = Infinity;
  for (const pkg of account.packages || []) {
    if (pkg.remain > 0 && pkg.expires_at) {
      const timestamp = new Date(pkg.expires_at).getTime();
      if (timestamp < earliest) earliest = timestamp;
    }
  }
  return { ...account, earliestExpiry: Number.isFinite(earliest) ? expiryDate(earliest) : '--' };
}));
async function loadPackages() {
  if (!workbuddyReady.value) return;
  packagesLoading.value = true;
  try { const response = await api('packages'); if (workbuddyReady.value) packages.value = response; }
  catch (cause) { if (workbuddyReady.value && cause.name !== 'AbortError') error.value = cause.message; }
  finally { packagesLoading.value = false; }
}
function expiryDate(value) {
  if (!value) return '--';
  const parsed = new Date(typeof value === 'number' ? value : String(value).replace(' ', 'T'));
  return Number.isNaN(parsed.getTime()) ? String(value) : parsed.toLocaleDateString('zh-CN', { timeZone: 'Asia/Shanghai' });
}
watch([() => props.section, workbuddyReady], ([section, ready]) => { if (section === 'rewards' && ready) loadPackages(); }, { immediate: true });
</script>

<template>
  <section :class="['benefits-view', 'benefits-' + section]">
    <header class="page-heading benefit-heading">
      <div class="page-toolbar"><slot name="platform-switcher" /><div class="heading-actions">
        <template v-if="section === 'tasks' && supported">
          <a-button :loading="working" :disabled="queue.running || !personal.length" @click="scanTasks"><template #icon><icon-search /></template>扫描全部待办</a-button>
          <a-button type="primary" :disabled="working || queue.running || !personal.length" @click="confirmTask(null)"><template #icon><icon-play-arrow /></template>执行全部待办</a-button>
        </template>
        <template v-else>
          <a-button v-if="supported" :loading="loading" :disabled="working" @click="refresh"><template #icon><icon-refresh /></template>刷新状态</a-button>
          <a-button v-if="section === 'rewards' && workbuddyReady" type="primary" :disabled="working || loading || queue.running || !claimCount" @click="confirmation = { title: '领取全部奖励', rewards: true }"><template #icon><icon-gift /></template>{{ claimCount ? `全部领取 (${claimCount})` : '全部领取' }}</a-button>
        </template>
      </div></div>
    </header>

    <a-alert v-if="error" type="error" class="benefit-notice">{{ error }}</a-alert>
    <a-alert v-if="result" type="info" class="benefit-notice">{{ result }}</a-alert>
    <a-alert v-if="unsupportedAccounts.length" type="warning" class="benefit-notice"><div v-for="item in unsupportedAccounts" :key="item.uid">{{ model.accountName(item) }}：{{ item.benefits_reason || '当前登录身份暂不支持个人任务和活动奖励' }}</div></a-alert>

    <a-spin :loading="loading" class="benefit-content">
      <template v-if="section === 'tasks'">
        <a-empty v-if="!supported" :description="model.providerName + ' 暂未接入成长任务'" />
        <template v-else>
          <section class="stat-strip task-stat-strip" aria-label="任务概览">
            <div class="stat-seg">
              <span>任务完成度</span>
              <strong>{{ completedTasks }} / {{ tasks.length }}</strong>
              <small>{{ taskProgressPercent }}% · {{ account ? model.accountName(account) : '当前账号' }}</small>
            </div>
            <i class="stat-divider" />
            <div class="stat-seg">
              <span>待处理</span>
              <strong :class="pendingTasksCount ? 'warning-text' : ''">{{ pendingTasksCount }}</strong>
              <small>{{ claimableTasks.length ? `${claimableTasks.length} 项可直接领取` : '暂无可领任务' }}</small>
            </div>
            <i class="stat-divider" />
            <div class="stat-seg">
              <span>执行队列</span>
              <strong :class="queue.running ? 'accent-text' : ''">{{ runningQueueCount }}</strong>
              <small>{{ queue.running ? `队列运行中 · ${done}/${queue.total || 0}` : '当前队列空闲' }}</small>
            </div>
            <i class="stat-divider" />
            <div class="stat-seg">
              <span>待领取积分</span>
              <strong :class="claimableCredits > 0 ? 'accent-text' : ''">{{ claimableCredits > 0 ? '+' + formatNumber(claimableCredits) : '0' }}</strong>
              <small>当前账号 · 已达成任务</small>
            </div>
          </section>

          <div class="benefit-layout tasks-layout">
            <section class="benefit-primary task-board">
              <div class="benefit-panel-heading">
                <div class="benefit-panel-title">
                  <h2>成长任务清单</h2>
                  <span>共 {{ tasks.length }} 项</span>
                </div>
                <div class="filter-tabs task-filter-tabs" role="group" aria-label="任务状态">
                  <button type="button" :class="{ active: filter === 'all' }" @click="filter = 'all'">全部 <span>{{ taskCounts.all }}</span></button>
                  <button type="button" :class="{ active: filter === 'claimable' }" @click="filter = 'claimable'">待领取 <span>{{ taskCounts.claimable }}</span></button>
                  <button type="button" :class="{ active: filter === 'pending' }" @click="filter = 'pending'">待执行 <span>{{ taskCounts.pending }}</span></button>
                  <button type="button" :class="{ active: filter === 'claimed' }" @click="filter = 'claimed'">已完成 <span>{{ taskCounts.claimed }}</span></button>
                  <button v-if="taskCounts.error" type="button" :class="{ active: filter === 'error' }" @click="filter = 'error'">失败 <span>{{ taskCounts.error }}</span></button>
                </div>
              </div>

              <div class="benefit-toolbar task-toolbar">
                <a-select :model-value="uid" :disabled="working" placeholder="选择账号" aria-label="查看账号" @change="selectAccount">
                  <a-option v-for="item in options" :key="item.uid" :value="item.uid">{{ model.accountName(item) }}</a-option>
                </a-select>
                <a-input v-model="search" allow-clear placeholder="搜索任务名称或代码" aria-label="搜索任务">
                  <template #prefix><icon-search /></template>
                </a-input>
                <a-button
                  v-if="claimableTasks.length"
                  type="primary"
                  size="small"
                  class="claim-all-btn"
                  :disabled="working || queue.running"
                  @click="claimAllTasks"
                >
                  <template #icon><icon-gift /></template>一键领取 ({{ claimableTasks.length }})
                </a-button>
                <a-button :loading="loading" :disabled="working" aria-label="刷新任务" title="刷新任务" @click="refresh">
                  <template #icon><icon-refresh /></template>
                </a-button>
              </div>

              <div class="task-progress-bar">
                <div class="inline-between">
                  <span>当前账号完成进度</span>
                  <strong>{{ completedTasks }} / {{ tasks.length }} · {{ taskProgressPercent }}%</strong>
                </div>
                <div class="streak-track"><i class="streak-track-fill" :style="{ width: taskProgressPercent + '%' }" /></div>
              </div>

              <div class="benefit-task-scroll">
                <div v-if="taskRows.length" class="task-row-list">
                  <article
                    v-for="record in taskRows"
                    :key="record.task_code"
                    :class="['task-item-row', { done: record.claimed, claimable: record.claimable, locked: record.locked }]"
                  >
                    <span :class="['task-symbol', record.claimed ? 'complete' : record.claimable ? 'available' : '']">
                      <icon-check v-if="record.claimed" />
                      <icon-gift v-else-if="record.claimable" />
                      <icon-thunderbolt v-else />
                    </span>

                    <div class="task-item-body">
                      <div class="task-item-head">
                        <strong>{{ record.title || record.task_code }}</strong>
                        <a-tag size="small" :color="taskState(record).color">{{ taskState(record).label }}</a-tag>
                        <span v-if="!record.claimed && automatic.includes(record.task_code)" class="auto-pill">可自动执行</span>
                      </div>
                      <p class="task-item-desc">{{ record.task_desc || record.description || record.task_code }}</p>
                      <div v-if="record.target" class="task-item-progress">
                        <div class="task-mini-track"><i :style="{ width: taskProgressRatio(record) + '%' }" /></div>
                        <span>{{ record.current }} / {{ record.target }}</span>
                      </div>
                    </div>

                    <div class="task-item-reward">
                      <strong class="accent-text">+{{ formatNumber(record.credit) }}</strong>
                      <small>积分<template v-if="record.energy"> · {{ record.energy }} 能量</template></small>
                    </div>

                    <div class="task-item-actions">
                      <a-button
                        v-if="record.claimable"
                        type="primary"
                        size="small"
                        :disabled="working || queue.running"
                        @click="execute('claim', '领取奖励', { task_code: record.task_code })"
                      >
                        <template #icon><icon-gift /></template>领取
                      </a-button>
                      <template v-else-if="!record.claimed && !record.locked">
                        <a-button
                          v-if="!record.accept_status || record.accept_status === 'not_accepted'"
                          size="small"
                          :disabled="working || queue.running"
                          @click="execute('accept', '接受任务', { task_codes: [record.task_code] })"
                        >
                          接受
                        </a-button>
                        <a-button
                          v-if="automatic.includes(record.task_code)"
                          type="secondary"
                          size="small"
                          :disabled="working || queue.running"
                          @click="confirmTask(record)"
                        >
                          <template #icon><icon-play-arrow /></template>执行
                        </a-button>
                      </template>
                      <span v-else-if="record.claimed" class="task-done-label">已完成</span>
                      <span v-else class="task-done-label">未解锁</span>
                    </div>
                  </article>
                </div>
                <a-empty v-else :description="account ? '暂无符合条件的任务' : '暂无可用的国内版个人账号'" />
              </div>

              <div class="benefit-panel-footer">
                <span><i class="status-dot" />{{ loadedAt ? '最近查询 ' + date(loadedAt).split(' ')[1] : '等待查询' }}</span>
                <span>显示 {{ taskRows.length }} / {{ tasks.length }} 项 · {{ account ? model.accountName(account) : '未选择账号' }}</span>
              </div>
            </section>

            <aside class="benefit-aside tasks-side">
              <section class="command-card queue-command-card" aria-label="批量执行队列">
                <div class="inline-between">
                  <span class="command-caption">批量执行队列</span>
                  <span :class="['side-badge', { off: !queue.running }]">
                    {{ queue.running ? '执行中' : '队列空闲' }}
                  </span>
                </div>

                <div class="queue-clock-row">
                  <strong>{{ done }} / {{ queue.total || 0 }}</strong>
                  <span>{{ queue.total ? `${queueProgressPercent}%` : '等待启动' }}</span>
                </div>

                <div class="queue-dark-track">
                  <i :style="{ width: queueProgressPercent + '%' }" />
                </div>

                <div v-if="queue.items?.length" class="queue-dark-list">
                  <div v-for="(item, index) in queue.items" :key="index" class="queue-dark-item">
                    <div class="inline-between">
                      <strong>{{ item.nickname || item.uid }}</strong>
                      <span :class="['queue-state-chip', item.status]">{{ statusName(item.status) }}</span>
                    </div>
                    <small>{{ item.code }}<template v-if="item.message"> · {{ item.message }}</template></small>
                  </div>
                </div>
                <p v-else class="queue-idle-hint">点击下方按钮自动扫描并依次执行全部个人账号的可执行待办任务。</p>

                <div class="concurrency-bar" role="group" aria-label="并行账号数">
                  <span class="concurrency-label">并行数</span>
                  <div class="concurrency-pills">
                    <button
                      v-for="n in [1, 2, 3, 4]"
                      :key="n"
                      type="button"
                      :class="['concurrency-pill', { active: concurrency === n }]"
                      :disabled="queue.running || working"
                      @click="concurrency = n"
                    >
                      {{ n }}
                    </button>
                  </div>
                </div>

                <a-button
                  long
                  type="primary"
                  class="comp-btn"
                  :disabled="working || queue.running || !personal.length"
                  @click="confirmTask(null)"
                >
                  <template #icon><icon-play-arrow /></template>
                  {{ queue.running ? '队列执行中…' : '开始执行队列' }}
                </a-button>
              </section>

              <section class="panel-card coverage-panel" aria-label="账号覆盖">
                <header class="ledger-head">
                  <div class="ledger-title">
                    <h2>账号覆盖</h2>
                    <small>
                      {{ scan ? `已扫描 · 共 ${scanPending} 项待办` : `${personal.length} 个账号可参与 · 点击切换` }}
                    </small>
                  </div>
                  <a href="#accounts" class="coverage-link">账号管理<icon-arrow-right /></a>
                </header>

                <div class="coverage-list">
                  <button
                    v-for="item in coverage"
                    :key="item.uid"
                    type="button"
                    :class="['coverage-item', { active: item.uid === uid, disabled: !isPersonalAccount(item) }]"
                    :disabled="!isPersonalAccount(item) || working"
                    @click="selectAccount(item.uid)"
                  >
                    <div class="inline-between">
                      <span class="account-caption">
                        <span class="account-initial">{{ model.accountName(item).slice(0, 1) }}</span>
                        <strong>{{ model.accountName(item) }}</strong>
                      </span>
                      <small :class="{ 'danger-text': item.pendingTasks?.growth_error, 'accent-text': item.uid === uid && tasks.length }">
                        {{ item.uid === uid && tasks.length ? completedTasks + ' / ' + tasks.length : item.disabled ? '已停用' : !isPersonalAccount(item) ? '暂不支持' : item.pendingTasks?.growth_error ? '查询失败' : item.pendingTasks ? (item.pendingTasks.growth || []).length + ' 项待办' : '待扫描' }}
                      </small>
                    </div>
                    <div v-if="item.uid === uid && tasks.length" class="task-mini-track">
                      <i :style="{ width: taskProgressPercent + '%' }" />
                    </div>
                  </button>
                </div>
              </section>
            </aside>
          </div>
        </template>
      </template>

      <template v-else>
        <template v-if="workbuddyReady">
          <div class="reward-account-bar">
            <div class="reward-account-picker">
              <span class="reward-account-label">查看账号</span>
              <a-select :model-value="uid" :disabled="working" placeholder="选择账号" aria-label="查看账号" @change="selectAccount">
                <a-option v-for="item in options" :key="item.uid" :value="item.uid">{{ model.accountName(item) }}</a-option>
              </a-select>
            </div>
            <div class="reward-account-meta">
              <span v-if="loadedAt" class="reward-sync-time"><i class="status-dot" />已同步 {{ date(loadedAt).split(' ')[1] }}</span>
              <span class="reward-account-count">{{ personal.length }} 个可用个人账号</span>
            </div>
          </div>

          <a-alert v-for="(message, name) in rewards?.errors || {}" :key="name" type="warning" class="benefit-notice">{{ name }}：{{ message }}</a-alert>

          <section class="stat-strip reward-stat-strip" aria-label="活动奖励概览">
            <div class="stat-seg">
              <span>待领取积分</span>
              <strong :class="rewardCredits > 0 ? 'accent-text' : ''">{{ rewardCredits > 0 ? '+' + formatNumber(rewardCredits) : '0' }}</strong>
              <small>{{ claimCount ? `${claimCount} 项奖励可直接领取` : '当前账号暂无待领奖励' }}</small>
            </div>
            <i class="stat-divider" />
            <div class="stat-seg">
              <span>连续登录</span>
              <strong>{{ streakHasData ? streakDays : '--' }} <small class="unit">天</small></strong>
              <small>补签卡 {{ makeupCards ?? '--' }} 张 · 当前账号</small>
            </div>
            <i class="stat-divider" />
            <div class="stat-seg">
              <span>抽奖机会</span>
              <strong :class="rewards?.chances > 0 ? 'accent-text' : ''">{{ rewards?.chances ?? '--' }} <small class="unit">次</small></strong>
              <small>{{ rewards?.chances > 0 ? '可立即参与活动抽奖' : '暂无可用抽奖次数' }}</small>
            </div>
            <i class="stat-divider" />
            <div class="stat-seg">
              <span>活动券码</span>
              <strong>{{ rewards?.vouchers?.length ?? 0 }} <small class="unit">张</small></strong>
              <small>{{ rewards?.vouchers?.length ? '点击右侧按钮即可复制' : '暂无已发放券码' }}</small>
            </div>
          </section>

          <div class="rewards-layout">
            <div class="rewards-main">
              <section class="panel-card streak-card" aria-label="连登里程碑">
                <header class="ledger-head streak-head">
                  <div class="ledger-title">
                    <h2>连登里程碑</h2>
                    <small>按当前账号连续登录天数解锁，已领取档位不可重复兑换</small>
                  </div>
                  <div class="streak-summary-pills">
                    <span class="streak-pill accent">连续 {{ streakHasData ? streakDays : '--' }} 天</span>
                    <span class="streak-pill">补签卡 {{ makeupCards ?? '--' }} 张</span>
                  </div>
                </header>

                <div class="streak-progress-block">
                  <div class="streak-progress-meta">
                    <span>周期进度</span>
                    <strong>{{ streakHasData ? `${Math.min(streakDays, maxTierDays)} / ${maxTierDays} 天` : '--' }}</strong>
                  </div>
                  <div class="streak-track" role="progressbar" :aria-valuenow="streakProgress" aria-valuemin="0" aria-valuemax="100">
                    <i class="streak-track-fill" :style="{ width: streakProgress + '%' }" />
                  </div>
                </div>

                <div v-if="streakTiers.length" class="tier-grid">
                  <article
                    v-for="tier in streakTiers"
                    :key="tier.tier"
                    :class="['tier-card', tierState(tier).kind]"
                  >
                    <div class="tier-card-top">
                      <span class="tier-days">{{ tier.days }} 天档</span>
                      <a-tag :color="tierState(tier).color">{{ tierState(tier).label }}</a-tag>
                    </div>
                    <div class="tier-credit">
                      <strong>+{{ formatNumber(tier.credit) }}</strong>
                      <small>积分</small>
                    </div>
                    <div class="tier-extras">
                      <span>{{ tier.energy || 0 }} 能量</span>
                      <span>{{ tier.chances || 0 }} 次抽奖</span>
                    </div>
                    <div class="tier-action">
                      <a-button
                        long
                        size="small"
                        :type="canRedeem(tier) ? 'primary' : 'secondary'"
                        :disabled="working || queue.running || !canRedeem(tier)"
                        @click="execute('reward', '兑换奖励', { action: 'redeem', tier: tier.tier })"
                      >
                        <template v-if="tierState(tier).kind === 'claimed'"><icon-check />已领取</template>
                        <template v-else-if="canRedeem(tier)"><icon-gift />立即兑换</template>
                        <template v-else-if="tierRemainingDays(tier) > 0">还差 {{ tierRemainingDays(tier) }} 天</template>
                        <template v-else>暂不可兑</template>
                      </a-button>
                    </div>
                  </article>
                </div>
                <a-empty v-else description="暂未返回连登档位信息" />
              </section>

              <section class="panel-card vouchers-card" aria-label="活动券码">
                <header class="ledger-head">
                  <div class="ledger-title">
                    <h2>活动券码</h2>
                    <small>共 {{ rewards?.vouchers?.length || 0 }} 张 · 有效期以平台返回为准</small>
                  </div>
                </header>
                <div v-if="rewards?.vouchers?.length" class="voucher-list">
                  <div v-for="voucher in rewards.vouchers" :key="voucher.grant_id" class="voucher-card-row">
                    <div class="voucher-main">
                      <strong>{{ voucher.prize_name || voucher.sku_code }}</strong>
                      <code class="voucher-code">{{ voucher.code }}</code>
                      <small>有效期至 {{ voucher.valid_to || '--' }}</small>
                    </div>
                    <a-button size="small" aria-label="复制券码" title="复制券码" @click="copy(voucher.code)">
                      <template #icon><icon-copy /></template>复制
                    </a-button>
                  </div>
                </div>
                <div v-else class="compact-empty">暂无已发放的活动券码</div>
              </section>
            </div>

            <aside class="rewards-side">
              <section class="command-card compensation-card" aria-label="官方补偿">
                <div class="inline-between">
                  <span class="command-caption">官方补偿</span>
                  <span :class="['side-badge', { off: !canCompensate }]">
                    {{ canCompensate ? '可领取' : rewards?.compensation?.claimed ? '已领取' : '暂无活动' }}
                  </span>
                </div>

                <div class="comp-value-row">
                  <strong>{{ canCompensate ? '+' + formatNumber(rewards.compensation.credit_num) : '0' }}</strong>
                  <span>积分</span>
                </div>

                <p class="comp-desc">
                  {{ !account ? '暂无可用的国内版个人账号' : !rewards?.compensation ? '暂未返回官方补偿状态' : rewards.compensation.active ? (rewards.compensation.claimed ? '当前账号本次服务异常补偿已领取。' : '当前账号有待领取的官方服务补偿，点击下方按钮即可到账。') : '当前无生效中的官方补偿活动。' }}
                </p>

                <div v-if="rewards?.compensation?.end_time" class="comp-deadline">
                  <icon-clock-circle />截止 {{ rewards.compensation.end_time }}
                </div>

                <a-button
                  long
                  type="primary"
                  class="comp-btn"
                  :disabled="working || queue.running || !canCompensate"
                  @click="execute('reward', '领取补偿', { action: 'compensation' })"
                >
                  <template #icon><icon-gift /></template>
                  {{ canCompensate ? '领取官方补偿' : rewards?.compensation?.claimed ? '已领取' : '暂无可领补偿' }}
                </a-button>
              </section>

              <section class="panel-card lottery-card" aria-label="活动抽奖">
                <header class="ledger-head">
                  <div class="ledger-title">
                    <h2>活动抽奖</h2>
                    <small>每次抽奖消耗 1 次机会</small>
                  </div>
                  <a-tag :color="rewards?.chances > 0 ? 'arcoblue' : 'gray'">{{ rewards?.chances > 0 ? '可参与' : '暂无机会' }}</a-tag>
                </header>
                <div class="lottery-body">
                  <div class="lottery-count">
                    <strong :class="rewards?.chances > 0 ? 'accent-text' : ''">{{ rewards?.chances ?? '--' }}</strong>
                    <span>剩余机会</span>
                  </div>
                  <a-button
                    type="primary"
                    :disabled="working || queue.running || !(rewards?.chances > 0)"
                    @click="execute('reward', '抽奖', { action: 'draw' })"
                  >
                    <template #icon><icon-gift /></template>抽奖一次
                  </a-button>
                </div>
              </section>

              <section v-if="gateway" class="panel-card batch-ops-card" aria-label="全账号快捷操作">
                <header class="ledger-head">
                  <div class="ledger-title">
                    <h2>全账号快捷操作</h2>
                    <small>立即触发后台批量巡检，进度可在运行日志查看</small>
                  </div>
                </header>
                <div class="batch-ops-list">
                  <div v-for="item in gatewayBatchActions" :key="item.path" class="batch-op-row">
                    <div class="batch-op-copy">
                      <strong>{{ item.label }}</strong>
                      <small>{{ item.desc }}</small>
                    </div>
                    <a-button
                      size="small"
                      :loading="gatewayWorking === item.path"
                      :disabled="!!gatewayWorking"
                      @click="gatewayBatch(item.path, item.label)"
                    >
                      立即执行
                    </a-button>
                  </div>
                </div>
              </section>
            </aside>
          </div>

          <section class="panel-card packages-card" aria-label="积分构成">
            <header class="ledger-head">
              <div class="ledger-title">
                <h2>积分构成</h2>
                <small>逐账号实时查询上游套餐包余量与最早到期时间</small>
              </div>
              <a-button size="small" :loading="packagesLoading" @click="loadPackages">
                <template #icon><icon-refresh /></template>刷新套餐包
              </a-button>
            </header>
            <div class="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>账号</th>
                    <th>剩余 / 总额度</th>
                    <th>余量占比</th>
                    <th>套餐包</th>
                    <th>最早到期</th>
                    <th>状态</th>
                  </tr>
                </thead>
                <tbody>
                  <template v-for="row in packageRows" :key="row.uid">
                    <tr :class="{ 'package-open': expandedPackage === row.uid }">
                      <td><strong>{{ model.accountName({ uid: row.uid, nickname: row.nickname }) }}</strong></td>
                      <td class="num-cell">
                        <strong class="accent-text">{{ formatNumber(row.remain) }}</strong>
                        <small v-if="row.size"> / {{ formatNumber(row.size) }}</small>
                      </td>
                      <td>
                        <div class="pkg-meter">
                          <div class="pkg-meter-track"><i :style="{ width: packageRatio(row.remain, row.size) + '%' }" /></div>
                          <span>{{ row.size ? packageRatio(row.remain, row.size) + '%' : '--' }}</span>
                        </div>
                      </td>
                      <td>
                        <span>{{ row.packages?.length || 0 }} 个</span>
                        <a-button
                          v-if="row.packages?.length"
                          type="text"
                          size="mini"
                          @click="expandedPackage = expandedPackage === row.uid ? '' : row.uid"
                        >
                          {{ expandedPackage === row.uid ? '收起' : '展开明细' }}
                        </a-button>
                      </td>
                      <td class="num-cell">{{ row.earliestExpiry }}</td>
                      <td>
                        <span :class="['pkg-status', row.error ? 'danger-text' : 'ok']">{{ row.error || '正常' }}</span>
                      </td>
                    </tr>
                    <tr v-if="expandedPackage === row.uid && row.packages?.length" class="package-detail-row">
                      <td colspan="6">
                        <div class="pkg-detail-grid">
                          <div v-for="(pkg, index) in row.packages" :key="index" class="pkg-detail-item">
                            <div class="inline-between">
                              <strong>{{ pkg.name }}</strong>
                              <span class="accent-text num-cell">{{ formatNumber(pkg.remain) }} / {{ formatNumber(pkg.size) }}</span>
                            </div>
                            <div class="pkg-meter-track"><i :style="{ width: packageRatio(pkg.remain, pkg.size) + '%' }" /></div>
                            <small>已用 {{ formatNumber(pkg.used) }}<template v-if="pkg.end_time"> · 失效 {{ expiryDate(pkg.end_time) }}</template></small>
                          </div>
                        </div>
                      </td>
                    </tr>
                  </template>
                </tbody>
              </table>
              <a-empty v-if="!packageRows.length && !packagesLoading" description="暂无账号套餐数据" />
            </div>
          </section>
        </template>
        <a-empty v-else :description="supported ? '添加 ' + model.providerName + ' 账号后即可领取活动奖励' : model.providerName + ' 暂未接入活动奖励'" />
      </template>
    </a-spin>
    <a-modal :visible="!!confirmation" :title="confirmation?.title" :footer="false" :mask-closable="false" @cancel="confirmation = null">
      <template v-if="confirmation?.rewards"><p>为「{{ account ? model.accountName(account) : '' }}」领取 {{ claimCount }} 项奖励，共 {{ formatNumber(rewardCredits) }} 积分。</p><p class="modal-description">包含当前可兑换的连登奖励和官方补偿。</p></template>
      <template v-else><p>{{ confirmation?.task?.title || '全部国内版个人账号的可执行待办' }}</p><p class="modal-description">任务可能发起真实对话并消耗积分，完成后会尝试领取奖励。</p></template>
      <div class="modal-actions"><a-button @click="confirmation = null">取消</a-button><a-button type="primary" :loading="working" @click="confirmExecution"><template #icon><icon-play-arrow /></template>{{ confirmation?.rewards ? '确认领取' : '确认执行' }}</a-button></div>
    </a-modal>
  </section>
</template>
