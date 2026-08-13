<template>
  <div class="codex-accounts-page">
    <div class="codex-accounts-card">
      <CodexAccountToolbar
        @oauth-login="showOAuthModal = true"
        @json-import="showJsonImportModal = true"
        @signup="showSignupModal = true"
        @bulk-delete="showBulkDeleteModal = true"
        @open-config="showGlobalConfigModal = true"
        @open-model-prices="showModelPricesModal = true"
      />

      <div class="codex-accounts-body">
        <CodexAccountList
          ref="accountListRef"
          @edit="handleEditAccount"
          @get-token="handleGetToken"
        />
      </div>
    </div>

    <CodexOAuthLoginModal
      v-model:show="showOAuthModal"
      @success="handleOAuthSuccess"
    />

    <CodexAccountEditModal
      v-model:show="showEditModal"
      :account="editingAccount"
      :restoring="editRestoring"
      @success="handleEditSuccess"
      @restore="handleRestoreAccount"
    />

    <CodexJsonImportModal
      v-model:show="showJsonImportModal"
      @success="handleJsonImportSuccess"
    />

    <CodexBulkDeleteModal
      v-model:show="showBulkDeleteModal"
      @success="handleBulkDeleteSuccess"
    />

    <CodexGlobalConfigModal
      v-model:show="showGlobalConfigModal"
    />

    <CodexModelPricesModal
      v-model:show="showModelPricesModal"
      @saved="handleModelPricesSaved"
    />

    <CodexSignupModal
      v-model:show="showSignupModal"
      @success="handleSignupSuccess"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useDialog, useMessage } from 'naive-ui'
import { useI18n } from 'vue-i18n'
import { useCodexAccountsStore } from '../../stores/codexAccountsStore'
import { codexApi } from '@/api/codex'
import type { CodexAccount, CodexAccountInput } from '@/types/codex'
import '@/styles/pages/codex.css'
import CodexAccountToolbar from './CodexAccountToolbar.vue'
import CodexAccountList from './CodexAccountList.vue'
import CodexOAuthLoginModal from './CodexOAuthLoginModal.vue'
import CodexAccountEditModal from './CodexAccountEditModal.vue'
import CodexJsonImportModal from './CodexJsonImportModal.vue'
import CodexBulkDeleteModal from './CodexBulkDeleteModal.vue'
import CodexGlobalConfigModal from './CodexGlobalConfigModal.vue'
import CodexModelPricesModal from './CodexModelPricesModal.vue'
import CodexSignupModal from './CodexSignupModal.vue'

const { t } = useI18n()
const message = useMessage()
const dialog = useDialog()
const codexStore = useCodexAccountsStore()

const showOAuthModal = ref(false)
const showEditModal = ref(false)
const showJsonImportModal = ref(false)
const showBulkDeleteModal = ref(false)
const showGlobalConfigModal = ref(false)
const showModelPricesModal = ref(false)
const showSignupModal = ref(false)
const editRestoring = ref(false)
const editingAccount = ref<CodexAccount | null>(null)
const accountListRef = ref<InstanceType<typeof CodexAccountList> | null>(null)

function toErrorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return String(error)
}

onMounted(async () => {
  try {
    await codexStore.loadAccounts(true)
  } catch (error) {
    message.error(t('codex.loadAccountsFailed') + ': ' + toErrorMessage(error))
  }
})

async function handleOAuthSuccess(accountData: CodexAccountInput) {
  try {
    await codexStore.addAccount(accountData)
    message.success(t('codex.accountAdded'))
  } catch (error) {
    message.error(t('codex.addAccountFailed') + ': ' + toErrorMessage(error))
  }
}

function handleEditAccount(account: CodexAccount): void {
  editingAccount.value = account
  showEditModal.value = true
}

async function handleEditSuccess(accountData: CodexAccountInput) {
  try {
    await codexStore.updateAccount(accountData)
    message.success(t('codex.accountUpdated'))
    showEditModal.value = false
    // Restore scroll position after update
    if (accountListRef.value?.restoreScrollPosition) {
      accountListRef.value.restoreScrollPosition()
    }
  } catch (error) {
    message.error(t('codex.updateAccountFailed') + ': ' + toErrorMessage(error))
  }
}

async function handleRestoreAccount(accountId: string) {
  editRestoring.value = true
  try {
    await codexStore.restoreAccount(accountId)
    message.success(t('codex.accountRestored'))
    showEditModal.value = false
    if (accountListRef.value?.restoreScrollPosition) {
      accountListRef.value.restoreScrollPosition()
    }
  } catch (error) {
    message.error(t('codex.restoreAccountFailed') + ': ' + toErrorMessage(error))
  } finally {
    editRestoring.value = false
  }
}

async function handleJsonImportSuccess() {
  // 批量导入结果 toast 已在 modal 内展示；store.importAccounts 成功后已刷新列表
}

async function handleBulkDeleteSuccess() {
  message.success(t('codex.bulkDeleteSuccess'))
  await codexStore.loadAccounts(true)
}

async function handleModelPricesSaved(): Promise<void> {
  try {
    await codexStore.loadAccounts(true)
  } catch (error) {
    message.error(`刷新预估成本失败：${toErrorMessage(error)}`)
  }
}

function handleGetToken(account: CodexAccount): void {
  const missingFields = [
    ['accountId', account.accountId],
    ['accessToken', account.accessToken],
    ['idToken', account.idToken],
    ['refreshToken', account.refreshToken]
  ]
    .filter(([, value]) => !String(value || '').trim())
    .map(([field]) => field)

  if (missingFields.length > 0) {
    message.error(`当前账号缺少字段：${missingFields.join(', ')}`)
    return
  }

  const displayName = account.email || account.accountId || account.id || '(未命名账号)'
  dialog.warning({
    title: '写入 auth.json',
    content: `确定将 ${displayName} 的 accountId/accessToken/idToken/refreshToken 写入 Codex auth.json 的 tokens 字段？这会覆盖现有 tokens 中对应字段。`,
    positiveText: t('common.confirm'),
    negativeText: t('common.cancel'),
    onPositiveClick: async () => {
      try {
        await codexApi.writeAccountToAuthJson(account)
        message.success('已写入 Codex auth.json')
      } catch (error) {
        message.error('写入 Codex auth.json 失败：' + toErrorMessage(error))
      }
    }
  })
}

async function handleSignupSuccess(accountData: CodexAccountInput) {
  try {
    await codexStore.addAccount(accountData)
    message.success(t('codex.accountAdded'))
  } catch (error) {
    message.error(t('codex.addAccountFailed') + ': ' + toErrorMessage(error))
  }
}
</script>
