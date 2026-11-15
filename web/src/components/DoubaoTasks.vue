<script setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { Alert as AAlert, Button as AButton, Empty as AEmpty, Input as AInput, Modal as AModal, Option as AOption, Progress as AProgress, Radio as ARadio, RadioGroup as ARadioGroup, Select as ASelect, Spin as ASpin, Table as ATable, TableColumn as ATableColumn, Tag as ATag, Message } from '@arco-design/web-vue';
import { IconRefresh, IconGift, IconCopy, IconCheck, IconThunderbolt, IconArrowRight, IconInfoCircle } from '@arco-design/web-vue/es/icon';
import { formatNumber } from '../useAccounts';

const props = defineProps({ model: Object });
const uid = ref(''), tasks = ref([]), total = ref(0), continuous = ref(0), days = ref(0);
const loading = ref(false), working = ref(''), error = ref(''), result = ref(''), loadedAt = ref(null);
const filter = ref('all'), search = ref(''), guide = ref(null);
const options = computed(() => props.model.accounts.filter(a => !a.disabled));
const account = computed(() => options.value.find(a => a.uid === uid.value));
const claimed = computed(() => tasks.value.filter(t => t.claimed).length);
const claimable = computed(() => tasks.value.filter(t => t.claimable && !t.claimed));
const reward = computed(() => claimable.value.reduce((sum, t) => sum + t.credit, 0));
const rows = computed(() => tasks.value.filter(t => (!search.value || `${t.title} ${t.description} ${t.task_code}`.toLowerCase().includes(search.value.toLowerCase())) && (filter.value === 'all' || filter.value === 'claimed' && t.claimed || filter.value === 'claimable' && t.claimable || filter.value === 'pending' && !t.claimed && !t.claimable)));
const state = task => task.claimed ? { label: '已领取', color: 'arcoblue' } : task.locked ? { label: '未解锁', color: 'gray' } : task.claimable ? { label: '待领取', color: 'orange' } : { label: task.status === 'in_progress' ? '进行中' : '待完成', color: 'gray' };
const abort = new AbortController();
let version = 0, disposed = false;
const api = (path, init = {}, selected = uid.value) => props.model.api(`doubao/accounts/${encodeURIComponent(selected)}/${path}`, { ...init, signal: abort.signal });
function apply(data) {
  tasks.value = data.tasks || []; total.value = data.total_amount || 0;
  continuous.value = data.continuous_days || 0; days.value = data.total_days || 0;
  loadedAt.value = new Date();
}
async function load() {
  const request = ++version;
  tasks.value = []; total.value = 0; continuous.value = 0; days.value = 0; loadedAt.value = null; error.value = ''; result.value = '';
  if (!account.value) { loading.value = false; return; }
  loading.value = true;
  try { const data = await api('tasks'); if (request === version) apply(data); }
  catch (cause) { if (request === version && cause.name !== 'AbortError') error.value = cause.message; }
  finally { if (request === version) loading.value = false; }
}
watch(() => options.value.map(a => a.uid).join(','), () => {
  if (!account.value) uid.value = options.value[0]?.uid || '';
}, { immediate: true });
watch(uid, () => { guide.value = null; filter.value = 'all'; search.value = ''; load(); }, { immediate: true });

async function perform(task, all = false) {
  if (working.value || loading.value || !account.value) return;
  const selected = uid.value;
  const selectedTasks = all ? [...claimable.value] : [task];
  if (!selectedTasks.length) return;
  working.value = all ? 'all' : task.task_code; error.value = ''; result.value = '';
  const failures = [];
  let completed = 0;
  try {
    for (const item of selectedTasks) {
      try {
        await api(item.kind === 'daily_signin' ? 'checkin' : 'tasks/claim', { method: 'POST', ...(item.kind === 'daily_signin' ? {} : { body: JSON.stringify({ task_code: item.task_code }) }) }, selected);
        completed++;
      } catch (cause) {
        if (cause.name === 'AbortError') return;
        failures.push(`${item.title}：${cause.message}`);
      }
    }
    // Refresh even after an uncertain claim response; never retry a mutation automatically.
    try { const data = await api('tasks', {}, selected); if (!disposed && selected === uid.value) apply(data); }
    catch (cause) { if (cause.name === 'AbortError') return; failures.push(`状态刷新失败：${cause.message}`); }
    try { await props.model.refreshOverview(); }
    catch (cause) { if (cause.name !== 'AbortError') failures.push(`账号刷新失败：${cause.message}`); }
    if (disposed || selected !== uid.value) return;
    result.value = completed ? all ? `已领取 ${completed} 项奖励` : task.kind === 'daily_signin' ? '签到成功' : '奖励已领取' : '';
    error.value = failures.join('；');
    if (failures.length) Message.warning('部分操作未完成，请查看详情');
    else Message.success(result.value);
  } finally { working.value = ''; }
}
async function copyPrompt(prompt) {
  try { await navigator.clipboard.writeText(prompt); Message.success('提示词已复制'); }
  catch { Message.error('复制失败，请手动选择提示词复制'); }
}
function refreshOnFocus() {
  if (!document.hidden && !working.value && !loading.value && (!loadedAt.value || Date.now() - loadedAt.value.getTime() > 15000)) load();
}
onMounted(() => { window.addEventListener('focus', refreshOnFocus); document.addEventListener('visibilitychange', refreshOnFocus); });
onBeforeUnmount(() => { disposed = true; ++version; abort.abort(); window.removeEventListener('focus', refreshOnFocus); document.removeEventListener('visibilitychange', refreshOnFocus); });
</script>

<template>
  <section class="benefits-view benefits-tasks doubao-tasks">
    <header class="page-heading benefit-heading"><div class="page-toolbar"><slot name="platform-switcher" /><div class="heading-actions">
      <a-button :loading="loading" :disabled="!!working || !account" @click="load"><template #icon><icon-refresh /></template>刷新任务</a-button>
      <a-button type="primary" :loading="working === 'all'" :disabled="!!working || loading || !claimable.length" @click="perform(null, true)"><template #icon><icon-gift /></template>领取可领奖励</a-button>
    </div></div></header>

    <a-alert v-if="error" type="error" class="benefit-notice">{{ error }}</a-alert>
    <a-alert v-if="result" type="success" class="benefit-notice">{{ result }}</a-alert>
    <a-spin :loading="loading" class="benefit-content">
      <div class="stats-band benefit-stats">
        <div class="metric"><span class="metric-label">任务完成度</span><strong>{{ claimed }} / {{ tasks.length }}</strong><small>当前账号 · 已领取奖励</small></div>
        <div class="metric"><span class="metric-label">待完成</span><strong class="warning-text">{{ tasks.filter(t => !t.claimed && !t.claimable && !t.locked).length }}</strong><small>{{ tasks.filter(t => t.locked).length }} 项尚未解锁</small></div>
        <div class="metric"><span class="metric-label">累计获得额度</span><strong>{{ formatNumber(total) }}</strong><small>活动累计值 · 非当前剩余额度</small></div>
        <div class="metric"><span class="metric-label">待领取额度</span><strong class="accent-text">+{{ formatNumber(reward) }}</strong><small>{{ claimable.length }} 项奖励可领取</small></div>
      </div>
      <div class="benefit-layout">
        <section class="benefit-primary">
          <div class="benefit-panel-heading"><div class="benefit-panel-title"><h2>豆包工作任务</h2><span>共 {{ tasks.length }} 项</span></div>
            <a-radio-group v-model="filter" type="button" size="small" aria-label="任务状态"><a-radio value="all">全部</a-radio><a-radio value="pending">待完成</a-radio><a-radio value="claimable">待领取</a-radio><a-radio value="claimed">已领取</a-radio></a-radio-group>
          </div>
          <div class="benefit-toolbar">
            <a-select v-model="uid" :disabled="!!working" placeholder="选择豆包账号" aria-label="查看豆包任务账号"><a-option v-for="item in options" :key="item.uid" :value="item.uid">{{ model.accountName(item) }}</a-option></a-select>
            <a-input v-model="search" allow-clear placeholder="搜索任务" aria-label="搜索豆包任务" />
          </div>
          <div class="benefit-task-scroll" tabindex="0" role="region" aria-label="豆包任务列表"><a-table class="benefit-task-table" :data="rows" row-key="task_code" :pagination="false" :bordered="false" :hoverable="false">
            <template #columns>
              <a-table-column title="任务" :width="300"><template #cell="{ record }"><div class="task-identity"><span :class="['task-symbol', record.claimed ? 'complete' : record.claimable ? 'available' : '']"><icon-check v-if="record.claimed" /><icon-gift v-else-if="record.claimable" /><icon-thunderbolt v-else /></span><div><strong>{{ record.title }}</strong><small class="benefit-detail">{{ record.description }}</small><small v-if="record.target" class="benefit-detail">进度 {{ formatNumber(record.current) }} / {{ formatNumber(record.target) }}</small></div></div></template></a-table-column>
              <a-table-column title="奖励" :width="100" align="right"><template #cell="{ record }"><strong class="accent-text">{{ record.credit ? '+' + formatNumber(record.credit) : '--' }}</strong><small class="benefit-detail">额度</small></template></a-table-column>
              <a-table-column title="状态" :width="100"><template #cell="{ record }"><a-tag :color="state(record).color">{{ state(record).label }}</a-tag></template></a-table-column>
              <a-table-column title="操作" :width="140"><template #cell="{ record }">
                <a-button v-if="record.claimable" type="text" :loading="working === record.task_code" :disabled="!!working || loading" @click="perform(record)">领取奖励</a-button>
                <a-button v-else-if="record.kind === 'daily_signin' && !record.claimed && !record.locked" type="text" :loading="working === record.task_code" :disabled="!!working || loading" @click="perform(record)">立即签到</a-button>
                <a-button v-else-if="!record.claimed && !record.locked" type="text" :disabled="!!working" @click="guide = record">查看指引 <icon-arrow-right /></a-button>
              </template></a-table-column>
            </template>
            <template #empty><a-empty :description="error ? '任务加载失败，请刷新重试' : account ? '暂无符合条件的任务' : '添加或启用豆包账号后即可查看任务'" /></template>
          </a-table></div>
          <div class="benefit-panel-footer"><span><i class="status-dot" />{{ loadedAt ? '最近查询 ' + loadedAt.toLocaleTimeString('zh-CN', { hour12: false }) : '等待查询' }}</span><span>{{ account ? model.accountName(account) : '未选择账号' }}</span></div>
        </section>
        <aside class="benefit-aside">
          <section class="benefit-side-section"><h2>每日签到</h2><div class="expiry-row"><span>连续签到</span><b>{{ continuous }} 天</b></div><div class="expiry-row"><span>累计签到</span><b>{{ days }} 天</b></div><p class="benefit-detail">签到与豆包客户端共用状态。平台返回错误时会显示原因，成功后回读任务状态。</p></section>
          <section class="benefit-side-section"><h2>完成与领奖</h2><p class="benefit-detail">工作领域、文档、表格、PPT 和定时提醒等任务，需在对应豆包账号内完成。回到此页刷新后，可领取平台已确认达成的奖励。</p><p class="benefit-detail">累计额度奖励达到门槛后解锁。任务、奖励金额与完成状态均以平台当前返回为准。</p><a href="https://www.doubao.com/chat/" target="_blank" rel="noopener noreferrer">打开豆包 <icon-arrow-right /></a></section>
          <section class="benefit-side-section"><h2>账号覆盖</h2><div v-for="item in model.accounts" :key="item.uid" class="coverage-line"><div class="inline-between"><span>{{ model.accountName(item) }}</span><small>{{ item.disabled ? '已停用' : item.uid === uid && loadedAt ? claimed + ' / ' + tasks.length : '切换账号查看' }}</small></div><a-progress v-if="item.uid === uid && tasks.length" :percent="claimed / tasks.length" :show-text="false" /></div><div class="benefit-side-note"><icon-info-circle />{{ options.length }} 个启用账号</div></section>
        </aside>
      </div>
    </a-spin>
    <a-modal :visible="!!guide" :title="guide?.title" :footer="false" @cancel="guide = null">
      <p>{{ guide?.description }}</p><p class="modal-description">请先确认豆包中登录的是「{{ account ? model.accountName(account) : '' }}」。在豆包客户端的「免费用豆包工作」活动中完成此项任务，返回面板刷新状态后领取奖励。</p>
      <div v-for="prompt in guide?.prompts || []" :key="prompt.prompt" class="doubao-task-prompt"><div class="inline-between"><strong>{{ prompt.show_prompt }}</strong><a-button type="text" size="small" @click="copyPrompt(prompt.prompt)"><template #icon><icon-copy /></template>复制提示词</a-button></div><p>{{ prompt.prompt }}</p></div>
      <div class="modal-actions"><a-button @click="guide = null">关闭</a-button><a class="doubao-open-link" href="https://www.doubao.com/chat/" target="_blank" rel="noopener noreferrer">打开豆包 <icon-arrow-right /></a></div>
    </a-modal>
  </section>
</template>

<style scoped>
.benefit-task-table { min-width: 640px; }
.doubao-task-prompt { margin: 16px 0; padding: 12px; border: 1px solid var(--border); border-radius: 8px; }
.doubao-task-prompt p { margin: 8px 0 0; color: var(--muted); white-space: pre-wrap; line-height: 1.6; }
.doubao-open-link { display: inline-flex; align-items: center; gap: 6px; padding: 7px 16px; border-radius: 6px; color: var(--accent); background: var(--accent-soft); }
</style>
