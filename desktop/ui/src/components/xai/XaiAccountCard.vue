<template>
  <div
    class="xai-account-card"
    :class="{
      active: isActive,
      banned: isBanned,
      cooldown: isCoolingDown
    }"
  >
    <div class="xai-card-header">
      <span class="account-email" :title="displayName">{{ displayName }}</span>
      <n-tag :type="statusType" size="small" round>{{ statusText }}</n-tag>
    </div>

    <div class="card-tags">
      <n-tag v-if="account.authKind" type="info" size="small">
        {{ authKindLabel }}
      </n-tag>
      <n-tag v-if="account.enabled === false" type="error" size="small">
        {{ t('xai.disabledLabel') }}
      </n-tag>
      <n-tag v-if="account.websockets" type="success" size="small">
        WS
      </n-tag>
      <n-tag v-if="isActive" size="tiny" type="success" round class="xai-badge-active">
        {{ t('xai.active') }}
      </n-tag>
    </div>

    <div class="card-body">
      <div class="meta-line">
        {{ t('xai.weight') }}: {{ account.weight || 1 }}
      </div>
      <div v-if="account.proxyUrl" class="proxy-info">
        {{ t('xai.proxy') }}: {{ truncateText(account.proxyUrl, 30) }}
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
          class="xai-action-btn xai-action-primary"
          :title="t('xai.activate')"
          :aria-label="t('xai.activate')"
          :disabled="busy"
          @click="emit('activate', account.id || '')"
        >
          <Power class="xai-action-icon" />
        </button>
        <button
          type="button"
          class="xai-action-btn"
          :title="t('xai.test')"
          :aria-label="t('xai.test')"
          :disabled="busy"
          @click="emit('test', account.id || '')"
        >
          <RefreshCw class="xai-action-icon" />
        </button>
        <button
          type="button"
          class="xai-action-btn"
          :title="t('xai.copy')"
          :aria-label="t('xai.copy')"
          :disabled="busy"
          @click="emit('copy', account)"
        >
          <Copy class="xai-action-icon" />
        </button>
        <button
          type="button"
          class="xai-action-btn"
          :title="t('xai.edit')"
          :aria-label="t('xai.edit')"
          :disabled="busy"
          @click="emit('edit', account)"
        >
          <Edit class="xai-action-icon" />
        </button>
        <button
          type="button"
          class="xai-action-btn xai-action-danger"
          :title="t('common.delete')"
          :aria-label="t('common.delete')"
          :disabled="busy"
          @click="confirmDelete"
        >
          <Trash class="xai-action-icon" />
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, onMounted, onBeforeUnmount } from 'vue'
import { NTag, useDialog } from 'naive-ui'
import { Power, RefreshCw, Copy, Edit, Trash } from 'lucide-vue-next'
import { useI18n } from 'vue-i18n'
import type { XaiAccount } from '@/types/xai'

const { t } = useI18n()
const dialog = useDialog()

const props = withDefaults(defineProps<{
  account: XaiAccount
  isActive?: boolean
  busy?: boolean
}>(), {
  isActive: false,
  busy: false
})

const emit = defineEmits<{
  activate: [accountId: string]
  test: [accountId: string]
  copy: [account: XaiAccount]
  edit: [account: XaiAccount]
  delete: [accountId: string]
}>()

const isCoolingDown = computed(() => (props.account.cooldownRemaining || 0) > 0)
const isBanned = computed(() => props.account.status === 'banned')
const nowTick = ref(Date.now())
let expireTickTimer: ReturnType<typeof setInterval> | null = null

const displayName = computed(() =>
  props.account.email || props.account.subject || props.account.id || truncateToken(props.account.refreshToken || props.account.apiKey)
)

const authKindLabel = computed(() => {
  if (props.account.authKind === 'api_key') return t('xai.authApiKey')
  return t('xai.authOAuth')
})

const statusType = computed<'default' | 'success' | 'warning' | 'error'>(() => {
  if (isCoolingDown.value) return 'warning'
  switch (props.account.status) {
    case 'valid': return 'success'
    case 'banned': return 'error'
    case 'exhausted': return 'warning'
    default: return 'default'
  }
})

const statusText = computed(() => {
  if (isCoolingDown.value) return cooldownText.value
  switch (props.account.status) {
    case 'valid': return t('xai.statusValid')
    case 'banned': return t('xai.statusBanned')
    case 'exhausted': return t('xai.statusExhausted')
    default: return t('xai.statusUnknown')
  }
})

const cooldownText = computed(() => {
  const remaining = props.account.cooldownRemaining
  if (!remaining || remaining <= 0) return ''
  const mins = Math.ceil(remaining / 60)
  const reason = props.account.cooldownReason || t('xai.cooldown')
  if (mins >= 60) {
    const h = Math.floor(mins / 60)
    const m = mins % 60
    return `${reason} ${h}h${m > 0 ? m + 'm' : ''}`
  }
  return `${reason} ${mins}m`
})

const expireInfo = computed(() => {
  void nowTick.value
  if (!props.account.expiresAt) return { text: '', isExpired: false }
  const expiresDate = new Date(props.account.expiresAt)
  const now = new Date(nowTick.value)
  if (isNaN(expiresDate.getTime())) return { text: '', isExpired: false }
  const diffMs = expiresDate.getTime() - now.getTime()
  if (diffMs <= 0) return { text: t('xai.tokenExpired'), isExpired: true }
  const diffMinutes = Math.floor(diffMs / (1000 * 60))
  const diffHours = Math.floor(diffMs / (1000 * 60 * 60))
  const diffDays = Math.floor(diffMs / (1000 * 60 * 60 * 24))
  if (diffDays > 0) return { text: `${diffDays}d`, isExpired: false }
  if (diffHours > 0) return { text: `${diffHours}h`, isExpired: false }
  return { text: `${diffMinutes}m`, isExpired: false }
})

const expireText = computed(() => (expireInfo.value.text ? `Token ${expireInfo.value.text}` : ''))
const isExpired = computed(() => expireInfo.value.isExpired)
const canActivate = computed(() => !props.isActive && props.account.status !== 'banned')

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
    content: t('xai.deleteConfirm'),
    positiveText: t('common.ok'),
    negativeText: t('common.cancel'),
    onPositiveClick: () => {
      emit('delete', props.account.id || '')
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
</script>

<style scoped>
.xai-account-card {
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

.xai-account-card:hover {
  box-shadow: var(--shadow-md, 0 4px 12px rgba(0, 0, 0, 0.1));
}

.xai-account-card.active {
  border-color: var(--accent, #0284c7);
  box-shadow: 0 0 0 3px rgba(102, 126, 234, 0.15);
}

.xai-account-card.banned {
  background: rgba(244, 67, 54, 0.05);
  border-color: rgba(244, 67, 54, 0.4);
}

.xai-account-card.banned:hover {
  box-shadow: 0 4px 12px rgba(244, 67, 54, 0.15);
}

.xai-account-card.cooldown {
  opacity: 0.7;
}

.xai-card-header {
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

.xai-badge-active {
  margin-left: auto;
}

.card-body {
  margin-bottom: 12px;
  flex: 1;
}

.meta-line,
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

.xai-action-btn {
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

.xai-action-btn:last-child {
  border-right: 0;
}

.xai-action-btn:hover:not(:disabled) {
  background: var(--bg-tertiary, #eef3f8);
  color: var(--text-primary, #1f2937);
}

.xai-action-btn:focus-visible {
  outline: 2px solid color-mix(in srgb, var(--accent, #0284c7) 35%, transparent);
  outline-offset: -2px;
}

.xai-action-btn:disabled {
  cursor: not-allowed;
  opacity: 0.45;
}

.xai-action-primary {
  color: var(--accent, #0284c7);
}

.xai-action-primary:hover:not(:disabled) {
  background: color-mix(in srgb, var(--accent, #0284c7) 10%, white);
}

.xai-action-danger {
  color: var(--danger, #dc2626);
}

.xai-action-danger:hover:not(:disabled) {
  background: color-mix(in srgb, var(--danger, #dc2626) 10%, white);
}

.xai-action-icon {
  width: 15px;
  height: 15px;
  flex-shrink: 0;
}
</style>
