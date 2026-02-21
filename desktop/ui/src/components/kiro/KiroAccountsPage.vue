<template>
  <div class="kiro-accounts-page">
    <div class="kiro-accounts-card">
      <KiroAccountToolbar
        @refresh="handleRefresh"
        @open-global-config="showGlobalConfigModal = true"
        @bulk-delete="showBulkDeleteModal = true"
        @add-kiro-sign="startKiroSignForAccount"
        @add-json="showJsonImportModal = true"
        @add-builder-idc="startBuilderIdcForAccount"
        @add-org-idc="startOrgIdcForAccount"
      />

      <div class="kiro-accounts-body">
        <KiroAccountList @edit-account="handleEditAccount" />
      </div>
    </div>

    <KiroAccountEditModal
      v-model:show="showEditModal"
      :account="editingAccount"
      @success="handleEditSubmit"
    />

    <KiroJsonImportModal
      v-model:show="showJsonImportModal"
      @success="handleJsonImport"
    />

    <KiroBulkDeleteModal
      v-model:show="showBulkDeleteModal"
      :accounts="kiroStore.accounts"
      @execute="handleBulkDelete"
    />

    <KiroGlobalConfigModal
      v-model:show="showGlobalConfigModal"
      @saved="handleGlobalConfigSaved"
    />

    <KiroIdcDeviceFlowModal
      :show="auth.idcDeviceModal.show"
      :verify-url="auth.idcDeviceModal.verifyUrl"
      :status-kind="auth.idcDeviceModal.statusKind"
      :status-text="auth.idcDeviceModal.statusText"
      :status-label="auth.idcDeviceModal.statusLabel"
      :loading="auth.idcDeviceModal.loading"
      @update:show="handleDeviceModalShowUpdate"
      @close="auth.closeIdcDeviceFlowDialog"
      @copy-link="handleCopyIdcVerifyUrl"
      @open-link="handleOpenIdcVerifyUrl"
    />

    <KiroIdcOrgLoginModal
      :show="auth.idcOrgModal.show"
      :step="auth.idcOrgModal.step"
      :start-url="auth.idcOrgModal.startUrl"
      :region="auth.idcOrgModal.region"
      :verify-url="auth.idcOrgModal.verifyUrl"
      :status-kind="auth.idcOrgModal.statusKind"
      :status-text="auth.idcOrgModal.statusText"
      :status-label="auth.idcOrgModal.statusLabel"
      :loading="auth.idcOrgModal.loading"
      @update:show="handleOrgModalShowUpdate"
      @update:start-url="(value) => (auth.idcOrgModal.startUrl = value)"
      @update:region="(value) => (auth.idcOrgModal.region = value)"
      @connect="handleSubmitIdcOrg"
      @back="auth.backToOrgLoginStep1"
      @close="auth.closeIdcOrgLoginDialog"
      @copy-link="handleCopyIdcVerifyUrl"
      @open-link="handleOpenIdcVerifyUrl"
    />

    <KiroSignLoginModal
      :show="auth.kiroSignModal.show"
      :waiting="auth.kiroSignModal.waiting"
      :login-url="auth.kiroSignModal.loginUrl"
      @update:show="handleSignModalShowUpdate"
      @close="auth.closeKiroSignLoginModal"
      @copy-link="handleCopyKiroSignUrl"
      @open-link="handleOpenKiroSignUrl"
      @open-incognito="handleOpenKiroSignUrlIncognito"
    />
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useDialog, useMessage } from 'naive-ui'
import { useI18n } from 'vue-i18n'
import { useKiroAccountsStore } from '@/stores/kiroAccountsStore'
import { useKiroConfigStore } from '@/stores/kiroConfigStore'
import { useKiroAuthFlows } from '@/composables/useKiroAuthFlows'
import type { KiroAccount, KiroAccountInput, KiroAuthCredential } from '@/types/kiro'
import '@/styles/pages/kiro.css'
import KiroAccountToolbar from './KiroAccountToolbar.vue'
import KiroAccountList from './KiroAccountList.vue'
import KiroAccountEditModal from './KiroAccountEditModal.vue'
import KiroJsonImportModal from './KiroJsonImportModal.vue'
import KiroBulkDeleteModal from './KiroBulkDeleteModal.vue'
import KiroGlobalConfigModal from './KiroGlobalConfigModal.vue'
import KiroIdcDeviceFlowModal from './KiroIdcDeviceFlowModal.vue'
import KiroIdcOrgLoginModal from './KiroIdcOrgLoginModal.vue'
import KiroSignLoginModal from './KiroSignLoginModal.vue'

const { t } = useI18n()
const message = useMessage()
const dialog = useDialog()

const kiroStore = useKiroAccountsStore()
const kiroConfigStore = useKiroConfigStore()

const showEditModal = ref(false)
const showJsonImportModal = ref(false)
const showBulkDeleteModal = ref(false)
const showGlobalConfigModal = ref(false)
const editingAccount = ref<KiroAccount | null>(null)

const auth = useKiroAuthFlows((type, text) => {
  if (type === 'success') message.success(text)
  else if (type === 'warning') message.warning(text)
  else if (type === 'info') message.info(text)
  else message.error(text)
})

function toErrorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return String(error)
}

async function loadAccountsWithFeedback(): Promise<void> {
  try {
    await kiroStore.loadAccounts()
  } catch (error) {
    message.error(t('kiro.loadAccountsFailed') + toErrorMessage(error))
  }
}

onMounted(async () => {
  await loadAccountsWithFeedback()
})

async function handleRefresh(): Promise<void> {
  await loadAccountsWithFeedback()
}

function handleEditAccount(account: KiroAccount): void {
  editingAccount.value = account
  showEditModal.value = true
}

async function handleEditSubmit(payload: KiroAccountInput): Promise<void> {
  if (!editingAccount.value) return

  try {
    await kiroStore.updateAccount(editingAccount.value.refreshToken, {
      ...editingAccount.value,
      ...payload
    })
    message.success(t('kiro.accountUpdated'))
  } catch (error) {
    message.error(t('kiro.updateAccountFailed') + toErrorMessage(error))
  }
}

async function handleJsonImport(accounts: KiroAccountInput[]): Promise<void> {
  let successCount = 0
  let failedCount = 0

  for (const account of accounts) {
    try {
      await kiroStore.addAccount(account)
      successCount += 1
    } catch {
      failedCount += 1
    }
  }

  message.success(
    `${t('kiro.jsonImportResultPrefix')}${successCount}${t('kiro.jsonImportResultMiddle')}${failedCount}${t('kiro.jsonImportResultSuffix')}`
  )
  await loadAccountsWithFeedback()
}

async function handleBulkDelete(refreshTokens: string[]): Promise<void> {
  if (!refreshTokens.length) {
    message.error(t('kiro.noAccountSelected'))
    return
  }

  const confirmed = await new Promise<boolean>((resolve) => {
    dialog.warning({
      title: t('common.confirm'),
      content: `${t('kiro.bulkDeleteConfirm')} (${refreshTokens.length})`,
      positiveText: t('common.confirm'),
      negativeText: t('common.cancel'),
      onPositiveClick: () => resolve(true),
      onNegativeClick: () => resolve(false)
    })
  })

  if (!confirmed) return

  let successCount = 0
  for (const refreshToken of refreshTokens) {
    try {
      await kiroStore.deleteAccount(refreshToken)
      successCount += 1
    } catch {
      // Continue deleting the remaining accounts.
    }
  }

  message.success(`${t('kiro.bulkDeleteSuccess')} (${successCount}/${refreshTokens.length})`)
  await loadAccountsWithFeedback()
}

async function addAccountFromCredential(credential: KiroAuthCredential): Promise<void> {
  const refreshToken = String(credential.refreshToken || '').trim()
  if (!refreshToken) {
    throw new Error(t('kiro.addAccountFailed') + ': empty refreshToken')
  }

  await kiroStore.addAccount({
    refreshToken,
    accessToken: credential.accessToken || '',
    profileArn: credential.profileArn || '',
    expiresAt: credential.expiresAt || '',
    region: credential.region || 'us-east-1',
    authMethod: credential.authMethod,
    provider: credential.provider || '',
    clientId: credential.clientId || '',
    clientSecret: credential.clientSecret || ''
  })
}

async function startBuilderIdcForAccount(): Promise<void> {
  const region = kiroConfigStore.config.region || 'us-east-1'
  await auth.startIdcDeviceFlowLogin(region, async (credential) => {
    await addAccountFromCredential({
      ...credential,
      authMethod: 'idc',
      provider: credential.provider || 'BuilderId'
    })
  })
}

function startOrgIdcForAccount(): void {
  const region = kiroConfigStore.config.region || 'us-east-1'
  auth.startIdcOrgLogin(region, async (credential) => {
    await addAccountFromCredential({
      ...credential,
      authMethod: 'idc',
      provider: credential.provider || 'Enterprise'
    })
  })
}

async function startKiroSignForAccount(): Promise<void> {
  try {
    await auth.startKiroSignLogin(async (credential) => {
      await addAccountFromCredential(credential)
    })
  } catch (error) {
    message.error(t('kiro.kiroSignLoginFailed') + ': ' + toErrorMessage(error))
  }
}

async function handleSubmitIdcOrg(): Promise<void> {
  try {
    await auth.submitIdcOrgLogin()
  } catch (error) {
    message.error(toErrorMessage(error))
  }
}

async function handleCopyIdcVerifyUrl(): Promise<void> {
  try {
    await auth.copyIdcVerifyUrl()
  } catch (error) {
    message.error(t('kiro.idcCopyFailed') + toErrorMessage(error))
  }
}

async function handleOpenIdcVerifyUrl(): Promise<void> {
  try {
    await auth.openIdcVerifyUrl()
  } catch (error) {
    message.error(t('kiro.idcCopyFailed') + toErrorMessage(error))
  }
}

async function handleCopyKiroSignUrl(): Promise<void> {
  try {
    await auth.copyKiroSignLoginUrl()
  } catch (error) {
    message.error(t('kiro.copyFailed') + ': ' + toErrorMessage(error))
  }
}

async function handleOpenKiroSignUrl(): Promise<void> {
  try {
    await auth.openKiroSignLoginUrl(false)
  } catch (error) {
    message.error(t('kiro.kiroSignLoginFailed') + ': ' + toErrorMessage(error))
  }
}

async function handleOpenKiroSignUrlIncognito(): Promise<void> {
  try {
    await auth.openKiroSignLoginUrl(true)
  } catch (error) {
    message.error(t('kiro.kiroSignLoginFailed') + ': ' + toErrorMessage(error))
  }
}

function handleDeviceModalShowUpdate(show: boolean): void {
  if (!show) auth.closeIdcDeviceFlowDialog()
}

function handleOrgModalShowUpdate(show: boolean): void {
  if (!show) auth.closeIdcOrgLoginDialog()
}

function handleSignModalShowUpdate(show: boolean): void {
  if (!show) {
    void auth.closeKiroSignLoginModal()
  }
}

function handleGlobalConfigSaved(): void {
  // No-op; success toast is shown inside modal.
}
</script>
