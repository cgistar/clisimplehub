<template>
  <div class="codex-account-toolbar">
    <div class="toolbar-left">
      <n-input
        v-model:value="searchQuery"
        :placeholder="t('codex.searchPlaceholder')"
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

      <n-space>
        <n-tag>{{ t('codex.total') }}: {{ accountCount.total }}</n-tag>
        <n-tag type="success">{{ t('codex.valid') }}: {{ accountCount.valid }}</n-tag>
        <n-tag type="error">{{ t('codex.banned') }}: {{ accountCount.banned }}</n-tag>
        <n-tag type="warning">{{ t('codex.exhausted') }}: {{ accountCount.exhausted }}</n-tag>
      </n-space>
    </div>

    <div class="toolbar-right">
      <n-button @click="handleRefresh" :loading="loading">
        <template #icon><n-icon><RefreshCw /></n-icon></template>
        {{ t('common.refresh') }}
      </n-button>
      <n-button @click="emit('open-config')">
        <template #icon><n-icon><Settings /></n-icon></template>
        {{ t('codex.config') }}
      </n-button>
      <n-button type="error" ghost @click="emit('bulk-delete')">
        <template #icon><n-icon><Trash2 /></n-icon></template>
        {{ t('codex.bulkDelete') }}
      </n-button>
      <n-dropdown :options="addAccountOptions" @select="handleAddAccountSelect">
        <n-button type="primary">
          <template #icon><n-icon><Plus /></n-icon></template>
          {{ t('codex.addAccount') }}
        </n-button>
      </n-dropdown>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { NInput, NSelect, NSpace, NTag, NButton, NDropdown, NIcon } from 'naive-ui'
import { Search, RefreshCw, Plus, Trash2, Settings } from 'lucide-vue-next'
import { useI18n } from 'vue-i18n'
import { storeToRefs } from 'pinia'
import { useCodexAccountsStore } from '../../stores/codexAccountsStore'

const { t } = useI18n()
const codexStore = useCodexAccountsStore()

const {
  searchQuery,
  filterStatus,
  accountCount,
  loading
} = storeToRefs(codexStore)

const emit = defineEmits<{
  'oauth-login': []
  'json-import': []
  'bulk-delete': []
  'open-config': []
}>()

const statusOptions = computed(() => [
  { label: t('codex.allAccounts'), value: 'all' },
  { label: t('codex.statusValid'), value: 'valid' },
  { label: t('codex.statusBanned'), value: 'banned' },
  { label: t('codex.statusExhausted'), value: 'exhausted' },
  { label: t('codex.rateLimit'), value: 'rate_limited' },
  { label: t('codex.cooling'), value: 'cooling' }
])

const addAccountOptions = computed(() => [
  {
    label: t('codex.oauthLogin'),
    key: 'oauth'
  },
  {
    label: t('codex.jsonImport'),
    key: 'json'
  }
])

async function handleRefresh(): Promise<void> {
  await codexStore.loadAccounts(true)
}

function handleAddAccountSelect(key: string | number): void {
  switch (key) {
    case 'oauth':
      emit('oauth-login')
      break
    case 'json':
      emit('json-import')
      break
  }
}
</script>

<style scoped>
.codex-account-toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 16px;
  background: var(--bg-secondary);
  border-bottom: 1px solid var(--border-color);
  gap: 16px;
  flex-wrap: wrap;
}

.toolbar-left {
  display: flex;
  align-items: center;
  gap: 12px;
  flex: 1;
  flex-wrap: wrap;
}

.toolbar-right {
  display: flex;
  align-items: center;
  gap: 8px;
}
</style>
