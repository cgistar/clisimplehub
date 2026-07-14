<template>
  <div class="xai-account-toolbar">
    <div class="toolbar-row toolbar-row-primary">
      <div class="toolbar-left">
        <n-input
          v-model:value="searchQuery"
          :placeholder="t('xai.searchPlaceholder')"
          clearable
          style="width: 240px"
        >
          <template #prefix>
            <n-icon><Search /></n-icon>
          </template>
        </n-input>

        <n-select
          v-model:value="filterStatus"
          :options="statusOptions"
          style="width: 150px"
        />

        <div class="auto-refresh-token-control">
          <n-switch
            :value="autoRefreshToken"
            :loading="autoRefreshSaving"
            @update:value="handleAutoRefreshTokenChange"
          />
          <span>{{ t('xai.autoRefreshToken') }}</span>
        </div>
      </div>

      <div class="toolbar-right">
        <n-button @click="handleRefresh" :loading="loading">
          <template #icon><n-icon><RefreshCw /></n-icon></template>
          {{ t('common.refresh') }}
        </n-button>
        <n-button
          quaternary
          circle
          :title="t('xai.cliConfigEditor')"
          @click="emit('open-cli-config')"
        >
          <template #icon><n-icon><Pencil /></n-icon></template>
        </n-button>
        <n-button @click="handleCopyVisibleAccounts" :disabled="filteredAccounts.length === 0">
          <template #icon><n-icon><Copy /></n-icon></template>
          {{ t('xai.copyVisibleAccounts') }}
        </n-button>
        <n-button @click="emit('open-config')">
          <template #icon><n-icon><Settings /></n-icon></template>
          {{ t('xai.config') }}
        </n-button>
        <n-button type="error" ghost @click="emit('bulk-delete')">
          <template #icon><n-icon><Trash2 /></n-icon></template>
          {{ t('xai.bulkDelete') }}
        </n-button>
        <n-dropdown :options="addAccountOptions" @select="handleAddAccountSelect">
          <n-button type="primary">
            <template #icon><n-icon><Plus /></n-icon></template>
            {{ t('xai.addAccount') }}
          </n-button>
        </n-dropdown>
      </div>
    </div>

    <div class="toolbar-row toolbar-row-meta">
      <n-space>
        <n-tag>{{ t('xai.total') }}: {{ accountCount.total }}</n-tag>
        <n-tag type="success">{{ t('xai.valid') }}: {{ accountCount.valid }}</n-tag>
        <n-tag type="error">{{ t('xai.banned') }}: {{ accountCount.banned }}</n-tag>
        <n-tag type="warning">{{ t('xai.exhausted') }}: {{ accountCount.exhausted }}</n-tag>
      </n-space>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { NInput, NSelect, NSpace, NTag, NButton, NDropdown, NIcon, NSwitch, useMessage } from 'naive-ui'
import { Search, RefreshCw, Plus, Trash2, Settings, Copy, Pencil } from 'lucide-vue-next'
import { useI18n } from 'vue-i18n'
import { storeToRefs } from 'pinia'
import { useXaiAccountsStore } from '../../stores/xaiAccountsStore'
import { buildXaiAccountsCopyJson } from '@/utils/xaiAccountCopy'
import { xaiApi } from '@/api/xai'

const { t } = useI18n()
const message = useMessage()
const xaiStore = useXaiAccountsStore()
const autoRefreshToken = ref(false)
const autoRefreshSaving = ref(false)

const {
  searchQuery,
  filterStatus,
  accountCount,
  loading,
  filteredAccounts
} = storeToRefs(xaiStore)

const emit = defineEmits<{
  'oauth-login': []
  'api-key': []
  'sso-import': []
  'json-import': []
  'bulk-delete': []
  'open-config': []
  'open-cli-config': []
}>()

const statusOptions = computed(() => [
  { label: t('xai.allAccounts'), value: 'all' },
  { label: t('xai.statusValid'), value: 'valid' },
  { label: t('xai.statusBanned'), value: 'banned' },
  { label: t('xai.statusExhausted'), value: 'exhausted' },
  { label: t('xai.cooling'), value: 'cooling' }
])

const addAccountOptions = computed(() => [
  { label: t('xai.oauthLogin'), key: 'oauth' },
  { label: t('xai.ssoImport'), key: 'sso' },
  { label: t('xai.apiKeyAdd'), key: 'api-key' },
  { label: t('xai.jsonImport'), key: 'json' }
])

async function handleRefresh(): Promise<void> {
  await xaiStore.loadAccounts(true)
}

async function loadAutoRefreshToken(): Promise<void> {
  try {
    const config = await xaiApi.getGlobalConfig()
    autoRefreshToken.value = Boolean(config.autoRefreshToken)
  } catch (error) {
    message.error(t('xai.autoRefreshTokenLoadFailed') + toErrorMessage(error))
  }
}

async function handleAutoRefreshTokenChange(enabled: boolean): Promise<void> {
  if (autoRefreshSaving.value) return
  autoRefreshSaving.value = true
  try {
    await xaiApi.setAutoRefreshToken(enabled)
    autoRefreshToken.value = enabled
    message.success(enabled ? t('xai.autoRefreshTokenEnabled') : t('xai.autoRefreshTokenDisabled'))
  } catch (error) {
    message.error(t('xai.autoRefreshTokenSaveFailed') + toErrorMessage(error))
  } finally {
    autoRefreshSaving.value = false
  }
}

onMounted(() => {
  void loadAutoRefreshToken()
})

function toErrorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return String(error)
}

async function handleCopyVisibleAccounts(): Promise<void> {
  try {
    await navigator.clipboard.writeText(buildXaiAccountsCopyJson(filteredAccounts.value))
    message.success(t('xai.copySuccess'))
  } catch (error) {
    message.error(t('xai.copyFailed') + toErrorMessage(error))
  }
}

function handleAddAccountSelect(key: string | number): void {
  switch (key) {
    case 'oauth':
      emit('oauth-login')
      break
    case 'api-key':
      emit('api-key')
      break
    case 'sso':
      emit('sso-import')
      break
    case 'json':
      emit('json-import')
      break
  }
}
</script>

<style scoped>
.xai-account-toolbar {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 16px;
  background: var(--bg-secondary);
  border-bottom: 1px solid var(--border-color);
}

.toolbar-row {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
}

.toolbar-row-primary {
  justify-content: space-between;
  flex-wrap: nowrap;
  min-width: 0;
}

.toolbar-left {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
  flex: 1 1 auto;
}

.auto-refresh-token-control {
  display: flex;
  align-items: center;
  gap: 8px;
  white-space: nowrap;
  color: var(--text-secondary);
  font-size: 13px;
}

.toolbar-right {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  flex: 0 0 auto;
  margin-left: auto;
  flex-wrap: nowrap;
}

.toolbar-row-meta {
  flex-wrap: wrap;
}
</style>
