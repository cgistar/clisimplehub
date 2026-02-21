<template>
  <div
    class="codex-account-card"
    :class="{
      active: isActive,
      banned: isBanned,
      cooldown: isCoolingDown
    }"
  >
    <div class="codex-card-header">
      <span class="account-email" :title="displayName">{{ displayName }}</span>
      <n-tag :type="statusType" size="small" round>{{ statusText }}</n-tag>
    </div>

    <div class="card-tags">
      <n-tag v-if="account.planType" type="info" size="small">
        {{ planTypeLabel }}
      </n-tag>
      <n-tag size="small">OpenAI</n-tag>
      <n-tag v-if="account.weight > 0" size="small">W:{{ account.weight }}</n-tag>
      <n-tag v-if="!hasRefreshToken" type="warning" size="small" :title="t('codex.noRefreshToken')">
        {{ t('codex.tempToken') }}
      </n-tag>
      <n-tag v-if="isActive" size="tiny" type="success" round class="codex-badge-active">
        {{ t('codex.active') }}
      </n-tag>
    </div>

    <div class="card-body">
      <CodexUsageBar
        v-if="account.codexUsage?.primary"
        :label="t('codex.usage5h')"
        :used-percent="account.codexUsage.primary.usedPercent"
        :remaining-seconds="account.codexUsage.primary.remainingSeconds"
      />
      <CodexUsageBar
        v-if="account.codexUsage?.secondary"
        :label="t('codex.usageWeek')"
        :used-percent="account.codexUsage.secondary.usedPercent"
        :remaining-seconds="account.codexUsage.secondary.remainingSeconds"
      />
      <div class="today-usage">
        {{ t('codex.todayUsage') }}: {{ account.todayRequests || 0 }}{{ t('codex.requestUnit') }} / {{ formatTokens(account.todayTotalTokens || 0) }}
      </div>
      <div v-if="account.proxyUrl" class="proxy-info">
        {{ t('codex.proxy') }}: {{ truncateText(account.proxyUrl, 30) }}
      </div>
    </div>

    <div class="card-footer">
      <div v-if="isCoolingDown" class="cooldown-info">
        {{ cooldownText }}
      </div>
      <div v-else class="expire-info" :class="{ expired: isExpired }">
        {{ expireText }}
      </div>
      <div class="card-actions">
        <button
          v-if="canActivate"
          type="button"
          class="codex-action-btn codex-action-primary"
          :title="t('codex.activate')"
          :aria-label="t('codex.activate')"
          @click="emit('activate', account.accountId)"
        >
          <Power class="codex-action-icon" />
        </button>
        <button
          type="button"
          class="codex-action-btn"
          :title="t('codex.test')"
          :aria-label="t('codex.test')"
          :disabled="!hasRefreshToken || isCoolingDown"
          @click="emit('test', account.accountId)"
        >
          <RefreshCw class="codex-action-icon" />
        </button>
        <button
          type="button"
          class="codex-action-btn"
          :title="t('codex.fetchUsage')"
          :aria-label="t('codex.fetchUsage')"
          :disabled="isCoolingDown"
          @click="emit('fetch-usage', account.accountId)"
        >
          <Activity class="codex-action-icon" />
        </button>
        <button
          type="button"
          class="codex-action-btn"
          :title="t('codex.copy')"
          :aria-label="t('codex.copy')"
          @click="emit('copy', account)"
        >
          <Copy class="codex-action-icon" />
        </button>
        <button
          type="button"
          class="codex-action-btn"
          :title="t('codex.edit')"
          :aria-label="t('codex.edit')"
          @click="emit('edit', account)"
        >
          <Edit class="codex-action-icon" />
        </button>
        <button
          type="button"
          class="codex-action-btn codex-action-danger"
          :title="t('common.delete')"
          :aria-label="t('common.delete')"
          @click="confirmDelete"
        >
          <Trash class="codex-action-icon" />
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { NTag, useDialog } from 'naive-ui'
import { Power, RefreshCw, Activity, Copy, Edit, Trash } from 'lucide-vue-next'
import { useI18n } from 'vue-i18n'
import type { CodexAccount } from '@/types/codex'
import CodexUsageBar from './CodexUsageBar.vue'

const { t } = useI18n()
const dialog = useDialog()

const props = withDefaults(defineProps<{
  account: CodexAccount
  isActive?: boolean
}>(), {
  isActive: false
})

const emit = defineEmits<{
  activate: [accountId: string]
  test: [accountId: string]
  'fetch-usage': [accountId: string]
  copy: [account: CodexAccount]
  edit: [account: CodexAccount]
  delete: [accountId: string]
}>()

const hasRefreshToken = computed(() => Boolean(props.account.refreshToken))
const isCoolingDown = computed(() => (props.account.cooldownRemaining || 0) > 0)
const isBanned = computed(() =>
  props.account.status === 'banned' || props.account.status === 'reused'
)

const displayName = computed(() =>
  props.account.email || props.account.accountId || truncateToken(props.account.refreshToken)
)

const planTypeLabel = computed(() => {
  const planType = props.account.planType
  if (!planType) return ''
  return planType.charAt(0).toUpperCase() + planType.slice(1)
})

const statusType = computed<'default' | 'success' | 'warning' | 'error'>(() => {
  if (isCoolingDown.value) return 'warning'
  switch (props.account.status) {
    case 'valid': return 'success'
    case 'banned':
    case 'reused': return 'error'
    case 'exhausted':
    case 'rate_limited': return 'warning'
    default: return 'default'
  }
})

const statusText = computed(() => {
  if (isCoolingDown.value) {
    return cooldownText.value
  }
  switch (props.account.status) {
    case 'valid': return t('codex.statusValid')
    case 'banned': return t('codex.statusBanned')
    case 'exhausted': return t('codex.statusExhausted')
    case 'reused': return t('codex.statusReused')
    case 'rate_limited': return t('codex.rateLimit')
    default: return t('codex.statusUnknown')
  }
})

const cooldownText = computed(() => {
  const remaining = props.account.cooldownRemaining
  if (!remaining || remaining <= 0) return ''
  const mins = Math.ceil(remaining / 60)
  const reason = props.account.cooldownReason === 'rate_limit'
    ? t('codex.rateLimit')
    : props.account.cooldownReason || t('codex.cooldown')

  if (mins >= 60) {
    const h = Math.floor(mins / 60)
    const m = mins % 60
    return `${reason} ${h}h${m > 0 ? m + 'm' : ''}`
  }
  return `${reason} ${mins}m`
})

const expireInfo = computed(() => {
  if (!props.account.expiresAt) return { text: '', isExpired: false }
  const expiresDate = new Date(props.account.expiresAt)
  const now = new Date()
  if (isNaN(expiresDate.getTime())) return { text: '', isExpired: false }

  const diffMs = expiresDate.getTime() - now.getTime()
  if (diffMs <= 0) return { text: t('codex.tokenExpired'), isExpired: true }

  const diffMinutes = Math.floor(diffMs / (1000 * 60))
  const diffHours = Math.floor(diffMs / (1000 * 60 * 60))
  const diffDays = Math.floor(diffMs / (1000 * 60 * 60 * 24))

  if (diffDays > 0) return { text: `${diffDays}d`, isExpired: false }
  if (diffHours > 0) return { text: `${diffHours}h`, isExpired: false }
  return { text: `${diffMinutes}m`, isExpired: false }
})

const expireText = computed(() => {
  return expireInfo.value.text ? `Token ${expireInfo.value.text}` : ''
})

const isExpired = computed(() => expireInfo.value.isExpired)

const canActivate = computed(() =>
  !props.isActive &&
  props.account.status !== 'banned' &&
  props.account.status !== 'reused'
)

function confirmDelete() {
  dialog.warning({
    title: t('common.confirm'),
    content: t('codex.deleteConfirm') || `Delete account ${displayName.value}?`,
    positiveText: t('common.ok'),
    negativeText: t('common.cancel'),
    onPositiveClick: () => {
      emit('delete', props.account.accountId)
    }
  })
}

function truncateToken(token?: string): string {
  if (!token || token.length <= 16) return token || ''
  return token.substring(0, 8) + '...' + token.substring(token.length - 8)
}

function truncateText(text: string | undefined, maxLength: number): string {
  if (!text || text.length <= maxLength) return text || ''
  return text.substring(0, maxLength) + '...'
}

function formatTokens(tokens: number | undefined): string {
  const num = Number(tokens) || 0
  if (num >= 1000000) return (num / 1000000).toFixed(1) + 'M'
  if (num >= 1000) return (num / 1000).toFixed(1) + 'K'
  return num.toString()
}
</script>

<style scoped>
.codex-account-card {
  background: var(--bg-primary);
  border: 2px solid var(--border-color);
  border-radius: var(--radius-lg, 8px);
  padding: 12px;
  transition: all 0.2s ease;
  display: flex;
  flex-direction: column;
  min-height: 220px;
  position: relative;
  justify-content: space-between;
}

.codex-account-card:hover {
  box-shadow: var(--shadow-md, 0 4px 12px rgba(0, 0, 0, 0.1));
}

.codex-account-card.active {
  border-color: var(--accent, #0284c7);
  box-shadow: 0 0 0 3px rgba(102, 126, 234, 0.15);
}

.codex-account-card.banned {
  background: rgba(244, 67, 54, 0.05);
  border-color: rgba(244, 67, 54, 0.4);
}

.codex-account-card.banned:hover {
  box-shadow: 0 4px 12px rgba(244, 67, 54, 0.15);
}

.codex-account-card.cooldown {
  opacity: 0.7;
}

.codex-card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 8px;
}

.account-email {
  flex: 1;
  min-width: 0;
  font-weight: 600;
  font-size: 13px;
  color: var(--text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  margin-right: 10px;
}

.card-tags {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-bottom: 12px;
  flex-wrap: wrap;
}

.codex-badge-active {
  margin-left: auto;
}

.card-body {
  margin-bottom: 12px;
  flex: 1;
}

.today-usage {
  font-size: 11px;
  color: var(--text-secondary);
  margin-top: 4px;
}

.proxy-info {
  font-size: 11px;
  color: var(--text-tertiary);
  margin-top: 4px;
}

.card-footer {
  margin-top: 6px;
  padding-top: 8px;
  border-top: 1px solid var(--border-color, #e0e0e0);
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
}

.cooldown-info {
  font-size: 12px;
  color: #f59e0b;
  font-weight: 500;
}

.expire-info {
  font-size: 12px;
  color: var(--text-tertiary);
}

.expire-info.expired {
  color: var(--danger, #dc2626);
  font-weight: 500;
}

.card-actions {
  margin-left: auto;
  display: inline-flex;
  align-items: center;
  border: 1px solid var(--border-color, #d5dee9);
  border-radius: 0;
  overflow: hidden;
  background: var(--bg-primary, #ffffff);
  flex-shrink: 0;
}

.codex-action-btn {
  appearance: none;
  border: 0;
  border-right: 1px solid var(--border-color, #d5dee9);
  border-radius: 0;
  background: transparent;
  color: var(--text-secondary, #536173);
  min-width: 32px;
  height: 28px;
  padding: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
}

.codex-action-btn:last-child {
  border-right: 0;
}

.codex-action-btn:hover:not(:disabled) {
  background: var(--bg-tertiary, #eef3f8);
  color: var(--text-primary, #1f2937);
}

.codex-action-btn:focus-visible {
  outline: 2px solid color-mix(in srgb, var(--accent, #0284c7) 35%, transparent);
  outline-offset: -2px;
}

.codex-action-btn:disabled {
  cursor: not-allowed;
  opacity: 0.45;
}

.codex-action-primary {
  color: var(--accent, #0284c7);
}

.codex-action-primary:hover:not(:disabled) {
  background: color-mix(in srgb, var(--accent, #0284c7) 10%, white);
}

.codex-action-danger {
  color: var(--danger, #dc2626);
}

.codex-action-danger:hover:not(:disabled) {
  background: color-mix(in srgb, var(--danger, #dc2626) 10%, white);
}

.codex-action-icon {
  width: 15px;
  height: 15px;
  flex-shrink: 0;
}
</style>
