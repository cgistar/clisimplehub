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
      <n-tag
        v-if="resetCreditsAvailableCount > 0"
        class="codex-reset-credit-badge"
        type="success"
        size="small"
        role="button"
        :tabindex="busy ? -1 : 0"
        :aria-disabled="busy"
        :title="t('codex.resetRateLimit')"
        :aria-label="t('codex.resetRateLimit')"
        @click="openResetCreditsDialog"
        @keydown.enter.prevent="openResetCreditsDialog"
        @keydown.space.prevent="openResetCreditsDialog"
      >
        {{ resetCreditsAvailableCount }}
      </n-tag>
      <n-tag v-if="!hasRefreshToken" type="warning" size="small" :title="t('codex.noRefreshToken')">
        {{ t('codex.tempToken') }}
      </n-tag>
      <n-tag v-if="account.enabled === false" type="error" size="small">
        {{ t('codex.disabledLabel') }}
      </n-tag>
      <n-tag v-if="account.websockets" type="success" size="small">
        WS
      </n-tag>
      <n-tag v-if="isActive" size="tiny" type="success" round class="codex-badge-active">
        {{ t('codex.active') }}
      </n-tag>
    </div>

    <div class="card-body">
      <CodexUsageBar
        :label="t('codex.usage5h')"
        :used-percent="account.codexUsage?.primary?.usedPercent ?? 0"
        :remaining-seconds="account.codexUsage?.primary?.remainingSeconds ?? 0"
        refreshable
        :refresh-disabled="busy"
        :refresh-title="t('codex.fetchUsage')"
        @refresh="emit('fetch-primary-usage', account.id)"
      />
      <CodexUsageBar
        :label="t('codex.usageWeek')"
        :used-percent="account.codexUsage?.secondary?.usedPercent ?? 0"
        :remaining-seconds="account.codexUsage?.secondary?.remainingSeconds ?? 0"
      />
      <div class="today-usage">
        <span>
          {{ account.todayRequests || 0 }}{{ t('codex.requestUnit') }}/{{ todayEstimatedCostText }} Token:{{ formatTokens(account.todayCachedTokens || 0) }}/{{ formatTokens(account.todayTotalTokens || 0) }}
          <!-- · {{ t('codex.reasoningTokens') }} {{ formatTokens(account.todayReasoningTokens || 0) }} -->
        </span>
        <span v-if="subscriptionActiveUntil" class="subscription-active-until">
          {{ subscriptionActiveUntil }}
        </span>
      </div>
      <div v-if="account.proxyUrl" class="proxy-info">
        {{ t('codex.proxy') }}: {{ truncateText(account.proxyUrl, 30) }}
      </div>
    </div>

    <div class="card-footer">
      <div class="expire-info" :class="{ expired: isExpired }">
        {{ expireText }}
      </div>
      <div class="card-actions">
        <button
          v-if="canActivate"
          type="button"
          class="codex-action-btn codex-action-primary"
          :title="t('codex.activate')"
          :aria-label="t('codex.activate')"
          :disabled="busy"
          @click="emit('activate', account.id)"
        >
          <Power class="codex-action-icon" />
        </button>
        <button
          type="button"
          class="codex-action-btn"
          :title="t('codex.test')"
          :aria-label="t('codex.test')"
          :disabled="busy || !hasRefreshToken"
          @click="emit('test', account.id)"
        >
          <RefreshCw class="codex-action-icon" />
        </button>
        <button
          type="button"
          class="codex-action-btn"
          :title="t('codex.fetchUsage')"
          :aria-label="t('codex.fetchUsage')"
          :disabled="busy"
          @click="emit('fetch-usage', account.id)"
        >
          <Activity class="codex-action-icon" />
        </button>
        <button
          type="button"
          class="codex-action-btn"
          :title="t('codex.copy')"
          :aria-label="t('codex.copy')"
          :disabled="busy"
          @click="emit('copy', account)"
        >
          <Copy class="codex-action-icon" />
        </button>
        <button
          type="button"
          class="codex-action-btn"
          :title="t('codex.getToken')"
          :aria-label="t('codex.getToken')"
          :disabled="busy"
          @click="emit('get-token', account)"
        >
          <KeyRound class="codex-action-icon" />
        </button>
        <button
          type="button"
          class="codex-action-btn"
          :title="t('codex.edit')"
          :aria-label="t('codex.edit')"
          :disabled="busy"
          @click="emit('edit', account)"
        >
          <Edit class="codex-action-icon" />
        </button>
        <button
          type="button"
          class="codex-action-btn codex-action-danger"
          :title="t('common.delete')"
          :aria-label="t('common.delete')"
          :disabled="busy"
          @click="confirmDelete"
        >
          <Trash class="codex-action-icon" />
        </button>
      </div>
    </div>

    <n-modal
      :show="resetCreditsVisible"
      preset="card"
      :title="t('codex.resetCreditsDialogTitle')"
      style="max-width: 520px; width: 90vw"
      @update:show="onResetCreditsVisibleChange"
    >
      <div class="reset-credits-body">
        <div v-if="resetCreditsLoading" class="reset-credits-state">
          <n-spin size="small" />
          <span>{{ t('codex.resetCreditsDialogLoading') }}</span>
        </div>
        <div v-else-if="resetCreditsError" class="reset-credits-state error">
          {{ resetCreditsError }}
        </div>
        <div v-else-if="!resetCredits.length" class="reset-credits-state empty">
          {{ t('codex.resetCreditsDialogEmpty') }}
        </div>
        <ul v-else class="reset-credits-list">
          <li
            v-for="item in resetCredits"
            :key="item.id"
            class="reset-credit-item"
            :class="{ disabled: !isCreditUsable(item) }"
          >
            <div class="reset-credit-main">
              <div class="reset-credit-title">{{ creditTitle(item) }}</div>
              <div v-if="creditDescription(item)" class="reset-credit-desc">{{ creditDescription(item) }}</div>
              <div class="reset-credit-meta">
                <span>{{ t('codex.resetCreditsType') }}: {{ item.reset_type || '-' }}</span>
                <span>{{ t('codex.resetCreditsStatus') }}: {{ creditStatusLabel(item) }}</span>
              </div>
              <div class="reset-credit-time">
                <span v-if="formatDate(item.granted_at)">{{ t('codex.resetCreditsGrantedAt') }}: {{ formatDate(item.granted_at) }}</span>
                <span v-if="formatDate(item.expires_at)">{{ t('codex.resetCreditsExpiresAt') }}: {{ formatDate(item.expires_at) }}</span>
              </div>
              <div v-if="!item.is_supported_by_plan" class="reset-credit-unsupported">
                {{ t('codex.resetCreditsUnsupported') }}
              </div>
            </div>
            <button
              type="button"
              class="reset-credit-btn"
              :disabled="busy || !isCreditUsable(item)"
              @click="confirmResetItem(item)"
            >
              {{ t('codex.resetCreditsResetAction') }}
            </button>
          </li>
        </ul>
      </div>
    </n-modal>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, onMounted, onBeforeUnmount } from 'vue'
import { NTag, NModal, NSpin, useDialog } from 'naive-ui'
import { Power, RefreshCw, Activity, Copy, KeyRound, Edit, Trash } from 'lucide-vue-next'
import { useI18n } from 'vue-i18n'
import { useCodexAccountsStore } from '../../stores/codexAccountsStore'
import type { CodexAccount, CodexResetCredit } from '@/types/codex'
import CodexUsageBar from './CodexUsageBar.vue'

const { t } = useI18n()
const dialog = useDialog()
const codexStore = useCodexAccountsStore()

const props = withDefaults(defineProps<{
  account: CodexAccount
  isActive?: boolean
  busy?: boolean
  resetCredit: (accountId: string, creditId: string) => Promise<void>
}>(), {
  isActive: false,
  busy: false
})

const emit = defineEmits<{
  activate: [accountId: string]
  test: [accountId: string]
  'fetch-usage': [accountId: string]
  'fetch-primary-usage': [accountId: string]
  copy: [account: CodexAccount]
  'get-token': [account: CodexAccount]
  edit: [account: CodexAccount]
  delete: [accountId: string]
}>()

const hasRefreshToken = computed(() => Boolean(props.account.refreshToken))
const isCoolingDown = computed(() => (props.account.cooldownRemaining || 0) > 0)
const isBanned = computed(() =>
  props.account.status === 'banned' || props.account.status === 'reused'
)
const nowTick = ref(Date.now())
let expireTickTimer: ReturnType<typeof setInterval> | null = null

const displayName = computed(() =>
  props.account.email || props.account.accountId || truncateToken(props.account.refreshToken)
)

const planTypeLabel = computed(() => {
  const planType = props.account.planType
  if (!planType) return ''
  return planType.charAt(0).toUpperCase() + planType.slice(1)
})

const resetCreditsAvailableCount = computed(() => {
  const count = Number(props.account.codexUsage?.resetCreditsAvailableCount ?? 0)
  return Number.isFinite(count) ? count : 0
})

const todayEstimatedCostText = computed(() => formatEstimatedCost(props.account.todayEstimatedCost))

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
  const reasonKey = String(props.account.cooldownReason || '')
  const isRateLimit = reasonKey === 'rate_limit'
    || reasonKey === 'websocket_rate_limit'
    || reasonKey === 'websocket_upstream_rate_limit'
  const label = isRateLimit ? t('codex.rateLimit') : t('codex.cooling')
  if (remaining < 60) {
    return `${label} ${remaining}s`
  }
  const mins = Math.ceil(remaining / 60)
  if (mins >= 60) {
    const h = Math.floor(mins / 60)
    const m = mins % 60
    return `${label} ${h}h${m > 0 ? m + 'm' : ''}`
  }
  return `${label} ${mins}m`
})

const expireInfo = computed(() => {
  void nowTick.value

  if (!props.account.expiresAt) return { text: '', isExpired: false }
  const expiresDate = new Date(props.account.expiresAt)
  const now = new Date(nowTick.value)
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

const subscriptionActiveUntil = computed(() => {
  return formatSubscriptionActiveUntil(getCodexSubscriptionActiveUntil(props.account.idToken))
})

onMounted(() => {
  expireTickTimer = setInterval(() => {
    nowTick.value = Date.now()
  }, 30 * 1000)
})

onBeforeUnmount(() => {
  if (expireTickTimer) {
    clearInterval(expireTickTimer)
    expireTickTimer = null
  }
})

function confirmDelete() {
  if (props.busy) return

  dialog.warning({
    title: t('common.confirm'),
    content: t('codex.deleteConfirm') || `Delete account ${displayName.value}?`,
    positiveText: t('common.ok'),
    negativeText: t('common.cancel'),
    onPositiveClick: () => {
      emit('delete', props.account.id)
    }
  })
}

const resetCreditsVisible = ref(false)
const resetCreditsLoading = ref(false)
const resetCreditsError = ref('')
const resetCredits = ref<CodexResetCredit[]>([])
let resetCreditsRequestGen = 0

async function openResetCreditsDialog(): Promise<void> {
  if (props.busy || resetCreditsAvailableCount.value <= 0) return
  resetCreditsVisible.value = true
  resetCreditsError.value = ''
  resetCreditsLoading.value = true
  resetCredits.value = []
  const gen = ++resetCreditsRequestGen
  try {
    const list = await codexStore.listResetCredits(props.account.id)
    if (gen !== resetCreditsRequestGen) return
    resetCredits.value = list?.credits ?? []
  } catch (cause) {
    if (gen !== resetCreditsRequestGen) return
    const detail = String(cause instanceof Error ? cause.message : cause)
    resetCreditsError.value = `${t('codex.resetCreditsDialogFailed')}: ${detail}`
  } finally {
    if (gen === resetCreditsRequestGen) {
      resetCreditsLoading.value = false
    }
  }
}

function onResetCreditsVisibleChange(show: boolean): void {
  if (!show) {
    closeResetCreditsDialog()
    return
  }
  resetCreditsVisible.value = true
}

function closeResetCreditsDialog(): void {
  resetCreditsVisible.value = false
  resetCreditsRequestGen += 1
  resetCreditsLoading.value = false
  resetCreditsError.value = ''
  resetCredits.value = []
}

function isCreditUsable(item: CodexResetCredit): boolean {
  return Boolean(item.id?.trim()) && Boolean(item.is_supported_by_plan) && item.status === 'available'
}

function creditTitle(item: CodexResetCredit): string {
  return (item.title && item.title.trim()) || t('codex.resetCreditsItemTitleFallback')
}

function creditDescription(item: CodexResetCredit): string {
  return (item.description && item.description.trim()) || ''
}

function creditStatusLabel(item: CodexResetCredit): string {
  if (item.status === 'available') return t('codex.resetCreditsAvailable')
  return item.status || '-'
}

function formatDate(value: string): string {
  if (!value) return ''
  const date = new Date(value)
  if (isNaN(date.getTime())) return ''
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')} ${String(date.getHours()).padStart(2, '0')}:${String(date.getMinutes()).padStart(2, '0')}`
}

function confirmResetItem(item: CodexResetCredit): void {
  if (props.busy || !isCreditUsable(item)) return
  const creditId = item.id.trim()
  dialog.warning({
    title: t('common.confirm'),
    content: t('codex.resetCreditsConfirmItem', { account: displayName.value }),
    positiveText: t('codex.resetCreditsResetAction'),
    negativeText: t('common.cancel'),
    onPositiveClick: async () => {
      try {
        await props.resetCredit(props.account.id, creditId)
        closeResetCreditsDialog()
      } catch {
        // 失败保留列表弹窗，便于重试
        return false
      }
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

function decodeBase64Url(value: string): string {
  const base64 = value.replace(/-/g, '+').replace(/_/g, '/')
  const padded = base64.padEnd(base64.length + ((4 - (base64.length % 4)) % 4), '=')
  const binary = atob(padded)
  const bytes = Uint8Array.from(binary, (char) => char.charCodeAt(0))
  return new TextDecoder().decode(bytes)
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value)
}

function getCodexSubscriptionActiveUntil(idToken?: string): string {
  if (!idToken) return ''

  const parts = idToken.split('.')
  if (parts.length < 2 || !parts[1]) return ''

  try {
    const claims: unknown = JSON.parse(decodeBase64Url(parts[1]))
    if (!isRecord(claims)) return ''

    const authClaims = claims['https://api.openai.com/auth']
    if (!isRecord(authClaims)) return ''

    const activeUntil = authClaims.chatgpt_subscription_active_until
    return typeof activeUntil === 'string' ? activeUntil : ''
  } catch {
    return ''
  }
}

function formatSubscriptionActiveUntil(value: string): string {
  const raw = value.trim()
  if (!raw) return ''

  const normalized = raw.replace(' ', 'T')
  const matched = normalized.match(/^(\d{4}-\d{2}-\d{2})T(\d{2})/)
  if (matched) return `${matched[1]}T${matched[2]}`

  const ms = Date.parse(raw)
  if (!Number.isFinite(ms)) return ''

  const date = new Date(ms)
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  const hour = String(date.getHours()).padStart(2, '0')
  return `${year}-${month}-${day}T${hour}`
}

function formatTokens(tokens: number | undefined): string {
  const num = Number(tokens) || 0
  if (num >= 1000000) return (num / 1000000).toFixed(1) + 'M'
  if (num >= 1000) return (num / 1000).toFixed(1) + 'K'
  return num.toString()
}

function formatEstimatedCost(cost: number | null | undefined): string {
  if (cost == null || !Number.isFinite(cost)) return '--'
  return `$${cost.toFixed(1)}`
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

.codex-reset-credit-badge {
  cursor: pointer;
}

.codex-reset-credit-badge:hover:not([aria-disabled='true']) {
  filter: brightness(0.96);
}

.codex-reset-credit-badge:focus-visible {
  outline: 2px solid color-mix(in srgb, var(--accent, #0284c7) 50%, transparent);
  outline-offset: 2px;
}

.codex-reset-credit-badge[aria-disabled='true'] {
  cursor: default;
  opacity: 0.45;
}

.codex-badge-active {
  margin-left: auto;
}

.card-body {
  margin-bottom: 12px;
  flex: 1;
}

.today-usage {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  font-size: 11px;
  color: var(--text-secondary);
  margin-top: 4px;
}

.subscription-active-until {
  color: var(--text-tertiary);
  flex-shrink: 0;
  font-variant-numeric: tabular-nums;
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

.reset-credits-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
  max-height: 60vh;
  overflow-y: auto;
  padding-right: 4px;
}

.reset-credits-state {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--text-secondary);
  font-size: 13px;
  padding: 16px 0;
  justify-content: center;
}

.reset-credits-state.error {
  color: var(--danger, #dc2626);
}

.reset-credits-state.empty {
  color: var(--text-tertiary);
}

.reset-credits-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.reset-credit-item {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 12px;
  border: 1px solid var(--border-color, #e0e0e0);
  border-radius: 6px;
  background: var(--bg-secondary, #f8fafc);
}

.reset-credit-item.disabled {
  opacity: 0.55;
  background: var(--bg-tertiary, #f1f5f9);
}

.reset-credit-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.reset-credit-title {
  font-weight: 600;
  font-size: 13px;
  color: var(--text-primary);
  word-break: break-word;
}

.reset-credit-desc {
  font-size: 12px;
  color: var(--text-secondary);
  word-break: break-word;
}

.reset-credit-meta,
.reset-credit-time {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  font-size: 11px;
  color: var(--text-tertiary);
}

.reset-credit-unsupported {
  font-size: 11px;
  color: var(--danger, #dc2626);
}

.reset-credit-btn {
  appearance: none;
  border: 1px solid var(--accent, #0284c7);
  background: transparent;
  color: var(--accent, #0284c7);
  border-radius: 4px;
  padding: 4px 12px;
  font-size: 12px;
  cursor: pointer;
  flex-shrink: 0;
}

.reset-credit-btn:hover:not(:disabled) {
  background: color-mix(in srgb, var(--accent, #0284c7) 10%, white);
}

.reset-credit-btn:disabled {
  cursor: not-allowed;
  opacity: 0.45;
  border-color: var(--border-color, #cbd5e1);
  color: var(--text-tertiary);
}
</style>
