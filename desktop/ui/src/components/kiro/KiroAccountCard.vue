<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { NTag, NBadge, NProgress } from 'naive-ui';
import { Power, Refresh, Battery, Copy, Edit, Trash } from '@vicons/tabler';
import type { KiroAccount } from '@/types/kiro';

const { t } = useI18n();

const props = withDefaults(defineProps<{
  account: KiroAccount
  isActive?: boolean
  busy?: boolean
}>(), {
  isActive: false,
  busy: false
});

const emit = defineEmits<{
  activate: [account: KiroAccount]
  test: [account: KiroAccount]
  usage: [account: KiroAccount]
  copy: [account: KiroAccount]
  edit: [account: KiroAccount]
  delete: [account: KiroAccount]
}>();

const statusType = computed<'default' | 'success' | 'warning' | 'error'>(() => {
  const status = String(props.account.status || '').toLowerCase();
  if (status === 'active' || status === 'valid') return 'success';
  if (status === 'warning' || status === 'exhausted') return 'warning';
  if (status === 'banned') return 'error';
  return 'default';
});

const usagePct = computed(() => {
  const current = props.account.currentUsage || 0;
  const limit = props.account.usageLimit || 1;
  return Math.min(100, (current / limit) * 100);
});

const usageStatus = computed<'default' | 'warning' | 'error'>(() => {
  if (usagePct.value > 90) return 'error';
  if (usagePct.value > 70) return 'warning';
  return 'default';
});

const expireInfo = computed(() => {
  if (!props.account.expiresAt) return null;
  const date = new Date(props.account.expiresAt);
  const now = new Date();
  const isExpired = date.getTime() < now.getTime();
  return {
    text: isExpired ? t('kiro.expired') : formatDate(date),
    isExpired
  };
});

function formatDate(date: Date): string {
  return new Intl.DateTimeFormat('default', {
    dateStyle: 'short',
    timeStyle: 'short'
  }).format(date);
}
</script>

<template>
  <div
    class="kiro-account-card"
    :class="{
      active: isActive,
      banned: account.status === 'banned'
    }"
  >
    <div class="kiro-card-header">
      <span class="kiro-account-email" :title="account.email">
        {{ account.email }}
      </span>
      <n-tag :type="statusType" size="small" round>
        {{ t(`kiro.status.${account.status}`) }}
      </n-tag>
    </div>

    <div class="kiro-header-tags">
      <n-tag v-if="account.subscriptionTitle" type="info" size="small">
        {{ account.subscriptionTitle }}
      </n-tag>
      <n-tag size="small">
        {{ account.authMethod }}
      </n-tag>
      <n-badge
        v-if="isActive"
        class="kiro-active-badge"
        :value="t('kiro.currentActive')"
        type="success"
      />
    </div>

    <div class="kiro-card-body">
      <div class="kiro-progress-section">
        <n-progress
          type="line"
          :percentage="usagePct"
          :show-indicator="false"
          :status="usageStatus"
        />
        <span class="kiro-progress-text">{{ usagePct.toFixed(0) }}%</span>
      </div>
      <div class="kiro-usage-meta">
        <span class="kiro-usage-nums">
          {{ account.currentUsage }} / {{ account.usageLimit }}
        </span>
        <span v-if="account.createdAt" class="kiro-added-date">
          {{ formatDate(new Date(account.createdAt)) }}
        </span>
      </div>
    </div>

    <div class="kiro-card-footer">
      <div class="kiro-expire-info" :class="{ expired: expireInfo?.isExpired }">
        {{ expireInfo?.text || '-' }}
      </div>
      <div class="kiro-card-actions">
        <button
          v-if="!isActive && account.status !== 'banned'"
          type="button"
          class="kiro-action-btn kiro-action-primary"
          :title="t('kiro.activate')"
          :aria-label="t('kiro.activate')"
          :disabled="busy"
          @click="emit('activate', account)"
        >
          <Power class="kiro-action-icon" />
        </button>

        <button
          type="button"
          class="kiro-action-btn"
          :title="t('kiro.test')"
          :aria-label="t('kiro.test')"
          :disabled="busy"
          @click="emit('test', account)"
        >
          <Refresh class="kiro-action-icon" />
        </button>

        <button
          type="button"
          class="kiro-action-btn"
          :title="t('kiro.fetchUsage')"
          :aria-label="t('kiro.fetchUsage')"
          :disabled="busy"
          @click="emit('usage', account)"
        >
          <Battery class="kiro-action-icon" />
        </button>

        <button
          type="button"
          class="kiro-action-btn"
          :title="t('kiro.copy')"
          :aria-label="t('kiro.copy')"
          :disabled="busy"
          @click="emit('copy', account)"
        >
          <Copy class="kiro-action-icon" />
        </button>

        <button
          type="button"
          class="kiro-action-btn"
          :title="t('kiro.edit')"
          :aria-label="t('kiro.edit')"
          :disabled="busy"
          @click="emit('edit', account)"
        >
          <Edit class="kiro-action-icon" />
        </button>

        <button
          type="button"
          class="kiro-action-btn kiro-action-danger"
          :title="t('kiro.delete')"
          :aria-label="t('kiro.delete')"
          :disabled="busy"
          @click="emit('delete', account)"
        >
          <Trash class="kiro-action-icon" />
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.kiro-account-card {
  background: var(--bg-secondary, #f9f9f9);
  border: 1px solid var(--border-color, #e0e0e0);
  border-radius: 8px;
  padding: 12px;
  transition: all 0.2s ease;
  width: 340px;
  height: 170px;
  display: flex;
  flex-direction: column;
  justify-content: flex-start;
}

.kiro-account-card.active {
  border-color: var(--primary-color, #4A90E2);
  box-shadow: 0 0 0 2px rgba(74, 144, 226, 0.1);
}

.kiro-account-card.banned {
  opacity: 0.6;
  background: var(--bg-error-light, #fef0f0);
}

.kiro-card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 8px;
}

.kiro-account-email {
  font-weight: 600;
  font-size: 14px;
  color: var(--text-primary, #333);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 200px;
}

.kiro-header-tags {
  display: flex;
  gap: 6px;
  margin-bottom: 12px;
  align-items: center;
  flex-wrap: wrap;
}

.kiro-active-badge {
  margin-left: auto;
}

.kiro-card-body {
  margin-bottom: 4px;
}

.kiro-progress-section {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 4px;
}

.kiro-progress-section :deep(.n-progress) {
  flex: 1;
}

.kiro-progress-text {
  font-size: 12px;
  color: var(--text-secondary, #666);
  font-family: 'SF Mono', 'Monaco', 'Consolas', monospace;
  min-width: 40px;
  text-align: right;
}

.kiro-usage-meta {
  display: flex;
  justify-content: space-between;
  font-size: 11px;
  color: var(--text-tertiary, #999);
}

.kiro-card-footer {
  margin-top: 4px;
  padding-top: 8px;
  border-top: 1px solid var(--border-color, #e0e0e0);
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
}

.kiro-expire-info {
  font-size: 11px;
  color: var(--text-tertiary, #999);
  flex-shrink: 0;
}

.kiro-expire-info.expired {
  color: var(--error-color, #f56c6c);
  font-weight: 600;
}

.kiro-card-actions {
  margin-left: auto;
  display: inline-flex;
  align-items: center;
  border: 1px solid var(--border-color, #d5dee9);
  border-radius: 0;
  overflow: hidden;
  background: var(--bg-primary, #ffffff);
  flex-shrink: 0;
}

.kiro-action-btn {
  appearance: none;
  border: 0;
  border-right: 1px solid var(--border-color, #d5dee9);
  border-radius: 0;
  background: transparent;
  color: var(--text-secondary, #536173);
  font-size: 11px;
  line-height: 1;
  min-width: 32px;
  height: 28px;
  padding: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
}

.kiro-action-btn:last-child {
  border-right: 0;
}

.kiro-action-btn:hover:not(:disabled) {
  background: var(--bg-tertiary, #eef3f8);
  color: var(--text-primary, #1f2937);
}

.kiro-action-btn:disabled {
  cursor: not-allowed;
  opacity: 0.45;
}

.kiro-action-btn:focus-visible {
  outline: 2px solid color-mix(in srgb, var(--accent, #0284c7) 35%, transparent);
  outline-offset: -2px;
}

.kiro-action-primary {
  color: var(--accent, #0284c7);
}

.kiro-action-primary:hover:not(:disabled) {
  background: color-mix(in srgb, var(--accent, #0284c7) 10%, white);
}

.kiro-action-danger {
  color: var(--danger, #dc2626);
}

.kiro-action-danger:hover:not(:disabled) {
  background: color-mix(in srgb, var(--danger, #dc2626) 10%, white);
}

.kiro-action-icon {
  width: 15px;
  height: 15px;
  flex-shrink: 0;
}

@media (max-width: 760px) {
  .kiro-account-card {
    width: 100%;
  }
}
</style>
