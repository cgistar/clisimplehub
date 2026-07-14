<template>
  <div class="xai-accounts-page">
    <div class="xai-accounts-card">
      <XaiAccountToolbar
        @oauth-login="showOAuthModal = true"
        @api-key="handleOpenApiKeyModal"
        @sso-import="showSSOImportModal = true"
        @json-import="showJsonImportModal = true"
        @bulk-delete="showBulkDeleteModal = true"
        @open-config="showGlobalConfigModal = true"
      />

      <div class="xai-accounts-body">
        <XaiAccountList
          ref="accountListRef"
          @edit="handleEditAccount"
        />
      </div>
    </div>

    <XaiOAuthLoginModal
      v-model:show="showOAuthModal"
      @success="handleOAuthSuccess"
    />

    <XaiAccountEditModal
      v-model:show="showEditModal"
      :account="editingAccount"
      :mode="editMode"
      @success="handleEditSuccess"
    />

    <XaiJsonImportModal
      v-model:show="showJsonImportModal"
      @success="handleJsonImportSuccess"
    />

    <XaiSSOImportModal
      v-model:show="showSSOImportModal"
      @success="handleSSOImportSuccess"
    />

    <XaiBulkDeleteModal
      v-model:show="showBulkDeleteModal"
      @success="handleBulkDeleteSuccess"
    />

    <XaiGlobalConfigModal
      v-model:show="showGlobalConfigModal"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useMessage } from 'naive-ui'
import { useI18n } from 'vue-i18n'
import { useXaiAccountsStore } from '../../stores/xaiAccountsStore'
import type { XaiAccount, XaiAccountInput } from '@/types/xai'
import '@/styles/pages/xai.css'
import XaiAccountToolbar from './XaiAccountToolbar.vue'
import XaiAccountList from './XaiAccountList.vue'
import XaiOAuthLoginModal from './XaiOAuthLoginModal.vue'
import XaiAccountEditModal from './XaiAccountEditModal.vue'
import XaiJsonImportModal from './XaiJsonImportModal.vue'
import XaiSSOImportModal from './XaiSSOImportModal.vue'
import XaiBulkDeleteModal from './XaiBulkDeleteModal.vue'
import XaiGlobalConfigModal from './XaiGlobalConfigModal.vue'

const { t } = useI18n()
const message = useMessage()
const xaiStore = useXaiAccountsStore()

const showOAuthModal = ref(false)
const showEditModal = ref(false)
const showJsonImportModal = ref(false)
const showSSOImportModal = ref(false)
const showBulkDeleteModal = ref(false)
const showGlobalConfigModal = ref(false)
const editMode = ref<'edit' | 'create-api-key'>('edit')
const editingAccount = ref<XaiAccount | null>(null)
const accountListRef = ref<InstanceType<typeof XaiAccountList> | null>(null)

function toErrorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return String(error)
}

onMounted(async () => {
  try {
    await xaiStore.loadAccounts(true)
  } catch (error) {
    message.error(t('xai.loadAccountsFailed') + toErrorMessage(error))
  }
})

function handleEditAccount(account: XaiAccount) {
  editMode.value = 'edit'
  editingAccount.value = account
  showEditModal.value = true
}

function handleOpenApiKeyModal() {
  editMode.value = 'create-api-key'
  editingAccount.value = null
  showEditModal.value = true
}

async function handleOAuthSuccess(payload: XaiAccountInput) {
  try {
    await xaiStore.addAccount(payload)
    message.success(t('xai.oauthLoginSuccess'))
  } catch (error) {
    message.error(t('xai.addAccountFailed') + toErrorMessage(error))
  }
}

async function handleEditSuccess(payload: XaiAccountInput) {
  try {
    if (editMode.value === 'create-api-key') {
      await xaiStore.addAccount(payload)
      message.success(t('xai.accountAdded'))
    } else {
      await xaiStore.updateAccount(payload)
      message.success(t('xai.accountUpdated'))
    }
  } catch (error) {
    message.error(
      (editMode.value === 'create-api-key' ? t('xai.addAccountFailed') : t('xai.updateAccountFailed')) +
        toErrorMessage(error)
    )
  }
}

function handleJsonImportSuccess() {
  void xaiStore.loadAccounts(true)
}

function handleSSOImportSuccess() {
  void xaiStore.loadAccounts(true)
}

function handleBulkDeleteSuccess() {
  void xaiStore.loadAccounts(true)
}
</script>
