<template>
  <n-modal
    v-model:show="visible"
    preset="card"
    :title="t('codex.bulkDeleteTitle')"
    style="width: 500px; max-height: 85vh"
  >
    <n-space vertical :size="12">
      <n-space>
        <n-button size="small" @click="toggleAll(true)">{{ t('codex.selectAll') }}</n-button>
        <n-button size="small" @click="toggleAll(false)">{{ t('codex.deselectAll') }}</n-button>
        <n-button size="small" type="warning" @click="selectBanned">
          {{ t('codex.selectBannedAccounts') }}
        </n-button>
      </n-space>

      <n-divider style="margin: 8px 0" />

      <div class="account-list">
        <n-checkbox-group v-model:value="selectedAccountIds">
          <n-space vertical :size="8">
            <div
              v-for="account in manageableAccounts"
              :key="account.accountId"
              class="account-item"
            >
              <n-checkbox :value="account.accountId" :label="getAccountLabel(account)">
                <template #default>
                  <div class="account-item-content">
                    <span class="account-label">{{ getAccountLabel(account) }}</span>
                    <n-tag :type="getStatusType(account)" size="small">
                      {{ getStatusText(account) }}
                    </n-tag>
                  </div>
                </template>
              </n-checkbox>
            </div>
          </n-space>
        </n-checkbox-group>
      </div>
    </n-space>

    <template #footer>
      <n-space justify="end">
        <n-button @click="handleCancel">{{ t('common.cancel') }}</n-button>
        <n-button
          type="error"
          @click="handleDelete"
          :loading="deleting"
          :disabled="selectedAccountIds.length === 0"
        >
          {{ t('common.delete') }}
        </n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { NModal, NSpace, NButton, NDivider, NCheckboxGroup, NCheckbox, NTag, useMessage, useDialog } from 'naive-ui'
import { useI18n } from 'vue-i18n'
import { storeToRefs } from 'pinia'
import { useCodexAccountsStore } from '../../stores/codexAccountsStore'
import type { CodexAccount } from '@/types/codex'

const { t } = useI18n()
const message = useMessage()
const dialog = useDialog()
const codexStore = useCodexAccountsStore()

const { accounts } = storeToRefs(codexStore)

const props = withDefaults(defineProps<{
  show: boolean
}>(), {
  show: false
})

const emit = defineEmits<{
  'update:show': [show: boolean]
  success: []
}>()

const visible = ref(false)
const selectedAccountIds = ref<string[]>([])
const deleting = ref(false)

const manageableAccounts = computed(() =>
  accounts.value.filter((account): account is CodexAccount & { accountId: string } => Boolean(account.accountId))
)

function toErrorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return String(error)
}

watch(() => props.show, (newVal) => {
  visible.value = newVal
  if (newVal) {
    selectedAccountIds.value = []
  }
})

watch(visible, (newVal) => {
  if (!newVal) {
    emit('update:show', false)
  }
})

function getAccountLabel(account: CodexAccount): string {
  return account.email || truncateToken(account.refreshToken)
}

function getStatusType(account: CodexAccount): 'default' | 'success' | 'warning' | 'error' {
  if (account.cooldownRemaining && account.cooldownRemaining > 0) return 'warning'
  switch (account.status) {
    case 'valid': return 'success'
    case 'banned':
    case 'reused': return 'error'
    case 'exhausted':
    case 'rate_limited': return 'warning'
    default: return 'default'
  }
}

function getStatusText(account: CodexAccount): string {
  if (account.cooldownRemaining && account.cooldownRemaining > 0) {
    const mins = Math.ceil(account.cooldownRemaining / 60)
    const reason = account.cooldownReason === 'rate_limit'
      ? t('codex.rateLimit')
      : account.cooldownReason || t('codex.cooldown')
    if (mins >= 60) {
      const h = Math.floor(mins / 60)
      const m = mins % 60
      return `${reason} ${h}h${m > 0 ? `${m}m` : ''}`
    }
    return `${reason} ${mins}m`
  }
  switch (account.status) {
    case 'valid': return t('codex.statusValid')
    case 'banned': return t('codex.statusBanned')
    case 'exhausted': return t('codex.statusExhausted')
    case 'reused': return t('codex.statusReused')
    case 'rate_limited': return t('codex.rateLimit')
    default: return t('codex.statusUnknown')
  }
}

function truncateToken(token?: string): string {
  if (!token || token.length <= 16) return token || ''
  return token.substring(0, 8) + '...' + token.substring(token.length - 8)
}

function toggleAll(checked: boolean): void {
  if (checked) {
    selectedAccountIds.value = manageableAccounts.value.map((account) => account.accountId)
  } else {
    selectedAccountIds.value = []
  }
}

function selectBanned(): void {
  selectedAccountIds.value = manageableAccounts.value
    .filter((account) => account.status === 'banned' || account.status === 'reused')
    .map((account) => account.accountId)
}

function handleCancel(): void {
  visible.value = false
}

async function handleDelete(): Promise<void> {
  if (selectedAccountIds.value.length === 0) {
    message.error(t('codex.selectAtLeastOne'))
    return
  }

  const confirmed = await new Promise<boolean>((resolve) => {
    dialog.warning({
      title: t('common.confirm'),
      content: t('codex.bulkDeleteConfirm').replace('{count}', String(selectedAccountIds.value.length)),
      positiveText: t('common.confirm'),
      negativeText: t('common.cancel'),
      onPositiveClick: () => resolve(true),
      onNegativeClick: () => resolve(false)
    })
  })

  if (!confirmed) return

  deleting.value = true
  try {
    const requestIDs = [...selectedAccountIds.value]
    await codexStore.deleteAccounts(requestIDs, { reload: false })
    message.success(`${t('codex.deletedCount')} ${requestIDs.length}/${requestIDs.length}`)
    visible.value = false
    emit('success')
  } catch (error) {
    message.error(t('codex.bulkDeleteFailed') + ': ' + toErrorMessage(error))
  } finally {
    deleting.value = false
  }
}
</script>

<style scoped>
.account-list {
  max-height: 400px;
  overflow-y: auto;
  padding: 8px;
}

.account-item {
  padding: 8px;
  border-bottom: 1px solid var(--border-light);
}

.account-item:last-child {
  border-bottom: none;
}

.account-item-content {
  display: flex;
  justify-content: space-between;
  align-items: center;
  width: 100%;
  margin-left: 8px;
}

.account-label {
  font-weight: 500;
  font-size: 13px;
}
</style>
