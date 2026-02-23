<template>
  <div class="codex-accounts-page">
    <div class="codex-accounts-card">
      <CodexAccountToolbar
        @oauth-login="showOAuthModal = true"
        @json-import="showJsonImportModal = true"
        @signup="showSignupModal = true"
        @bulk-delete="showBulkDeleteModal = true"
        @open-config="showGlobalConfigModal = true"
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
      @success="handleEditSuccess"
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

    <CodexGetTokenModal
      v-model:show="showGetTokenModal"
      :account="getTokenAccount"
      @success="handleGetTokenSuccess"
      @status-update="handleGetTokenStatusUpdate"
    />

    <CodexSignupModal
      v-model:show="showSignupModal"
      @success="handleSignupSuccess"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useMessage } from 'naive-ui'
import { useI18n } from 'vue-i18n'
import { useCodexAccountsStore } from '../../stores/codexAccountsStore'
import type { CodexAccount, CodexAccountInput } from '@/types/codex'
import '@/styles/pages/codex.css'
import CodexAccountToolbar from './CodexAccountToolbar.vue'
import CodexAccountList from './CodexAccountList.vue'
import CodexOAuthLoginModal from './CodexOAuthLoginModal.vue'
import CodexAccountEditModal from './CodexAccountEditModal.vue'
import CodexJsonImportModal from './CodexJsonImportModal.vue'
import CodexBulkDeleteModal from './CodexBulkDeleteModal.vue'
import CodexGlobalConfigModal from './CodexGlobalConfigModal.vue'
import CodexGetTokenModal from './CodexGetTokenModal.vue'
import CodexSignupModal from './CodexSignupModal.vue'

const { t } = useI18n()
const message = useMessage()
const codexStore = useCodexAccountsStore()

const showOAuthModal = ref(false)
const showEditModal = ref(false)
const showJsonImportModal = ref(false)
const showBulkDeleteModal = ref(false)
const showGlobalConfigModal = ref(false)
const showGetTokenModal = ref(false)
const showSignupModal = ref(false)
const editingAccount = ref<CodexAccount | null>(null)
const getTokenAccount = ref<CodexAccount | null>(null)
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

async function handleJsonImportSuccess() {
  message.success(t('codex.importSuccess'))
  await codexStore.loadAccounts(true)
}

async function handleBulkDeleteSuccess() {
  message.success(t('codex.bulkDeleteSuccess'))
  await codexStore.loadAccounts(true)
}

function handleGetToken(account: CodexAccount): void {
  getTokenAccount.value = account
  showGetTokenModal.value = true
}

async function handleGetTokenSuccess(payload: CodexAccountInput) {
  try {
    await codexStore.updateAccount(payload)
    message.success(t('codex.getTokenSuccess'))
    // Restore scroll position after update
    if (accountListRef.value?.restoreScrollPosition) {
      accountListRef.value.restoreScrollPosition()
    }
  } catch (error) {
    message.error(t('codex.updateAccountFailed') + ': ' + toErrorMessage(error))
  }
}

async function handleGetTokenStatusUpdate(payload: CodexAccountInput) {
  try {
    await codexStore.updateAccount(payload)
    message.warning(t('codex.accountUpdated'))
    // Restore scroll position after update
    if (accountListRef.value?.restoreScrollPosition) {
      accountListRef.value.restoreScrollPosition()
    }
  } catch (error) {
    message.error(t('codex.updateAccountFailed') + ': ' + toErrorMessage(error))
  }
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
