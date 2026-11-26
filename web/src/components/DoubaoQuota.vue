<script setup>
import { computed } from 'vue';
import { Progress as AProgress } from '@arco-design/web-vue';
import { accountCredits, formatNumber } from '../useAccounts';

const props = defineProps({ account: Object, part: { type: String, default: 'all' } });
const date = value => new Date(value).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' });
const usedRatio = quota => quota.total > 0 ? Math.min(1, Math.max(0, quota.used / quota.total)) : 0;
const quotas = computed(() => props.account.quotas || []);
const mainQuotas = computed(() => quotas.value.filter(quota => quota.kind !== 'subscription'));
const subscriptionQuotas = computed(() => quotas.value.filter(quota => quota.kind === 'subscription'));
</script>

<template>
  <!-- 桌面端：方案徽章移至账号列，剩余数字不再重复展示（可用额度见顶部统计）。 -->
  <div v-if="part === 'main'" class="doubao-quota-list">
    <div v-for="(quota, index) in mainQuotas" :key="index" class="doubao-quota">
      <div class="quota-heading"><span>{{ quota.name }}</span></div>
      <a-progress :percent="usedRatio(quota)" :show-text="false" size="small" :stroke-width="6" :color="account.disabled ? 'var(--muted)' : 'var(--accent)'" />
      <small>已用 {{ formatNumber(quota.used) }} / {{ formatNumber(quota.total) }}</small>
      <small v-if="quota.expires_at">{{ date(quota.expires_at) }} 到期</small>
    </div>
    <div v-if="!mainQuotas.length" class="credit-amount"><b>{{ accountCredits(account) }}</b><small>{{ account.balance_known ? '暂无活动额度' : '尚未获取额度' }}</small></div>
    <small v-if="account.balance_updated_at && account.balance_known" class="quota-updated"><i class="quota-ok" />{{ account.last_error ? '上次成功' : '更新于' }} {{ date(account.balance_updated_at) }}</small>
  </div>

  <!-- 桌面端：按上游实际类型展示套餐额度和滚动周期上限。 -->
  <div v-else-if="part === 'subscription'" class="doubao-quota-list">
    <template v-if="subscriptionQuotas.length">
      <div v-for="(quota, index) in subscriptionQuotas" :key="index" class="doubao-quota">
        <div class="quota-heading"><span>{{ quota.name }}</span></div>
        <a-progress :percent="usedRatio(quota)" :show-text="false" size="small" :stroke-width="6" :color="account.disabled ? 'var(--muted)' : 'var(--accent)'" />
        <small>已用 {{ formatNumber(quota.used) }} / {{ formatNumber(quota.total) }}</small>
        <small v-if="quota.expires_at">{{ date(quota.expires_at) }} 到期</small>
        <small v-else-if="quota.starts_at">{{ date(quota.starts_at) }} 开始</small>
        <small v-else-if="quota.window_type === 2">开始使用后计时</small>
      </div>
    </template>
    <span v-else class="quota-empty">—</span>
  </div>

  <!-- 移动端与通用：完整堆叠（方案徽章已在账号列展示） -->
  <div v-else class="doubao-quota-list">
    <div class="quota-heading"><span>可用额度</span><b>{{ accountCredits(account) }}</b></div>
    <div v-for="(quota, index) in quotas" :key="index" class="doubao-quota">
      <div class="quota-heading"><span>{{ quota.name }}</span><span>剩余 <b>{{ formatNumber(quota.remaining) }}</b></span></div>
      <a-progress :percent="usedRatio(quota)" :show-text="false" size="small" :stroke-width="6" :color="account.disabled ? 'var(--muted)' : 'var(--accent)'" />
      <small>已用 {{ formatNumber(quota.used) }} / {{ formatNumber(quota.total) }}</small>
      <small v-if="quota.expires_at">{{ date(quota.expires_at) }} 到期</small>
      <small v-else-if="quota.starts_at">{{ date(quota.starts_at) }} 开始</small>
      <small v-else-if="quota.window_type === 2">开始使用后计时</small>
    </div>
    <div v-if="!quotas.length" class="credit-amount"><b>{{ accountCredits(account) }}</b><small>{{ account.balance_known ? '暂无活动额度' : '尚未获取额度' }}</small></div>
    <small v-if="account.balance_updated_at && account.balance_known" class="quota-updated"><i class="quota-ok" />{{ account.last_error ? '上次成功' : '更新于' }} {{ date(account.balance_updated_at) }}</small>
  </div>
</template>

<style scoped>
.doubao-quota-list { display: grid; gap: 12px; min-width: 200px; width: 100%; }
.doubao-quota { display: grid; gap: 4px; }
.quota-heading { display: flex; justify-content: space-between; align-items: baseline; gap: 12px; font-size: 12px; }
.quota-heading b { color: var(--text); font-size: 16px; font-variant-numeric: tabular-nums; }
.doubao-quota-list small { color: var(--muted); font-size: 11px; line-height: 1.5; }
.quota-empty { color: var(--muted); font-size: 13px; line-height: 1.5; }
.quota-updated { display: inline-flex; align-items: center; gap: 5px; margin-top: 2px; }
.quota-ok { width: 6px; height: 6px; border-radius: 3px; background: var(--color-success-6, #00b42a); flex-shrink: 0; }
</style>
