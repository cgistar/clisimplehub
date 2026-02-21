<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { NInput, NSelect, NButton, NSpace, NTag, NIcon, NDropdown } from 'naive-ui'
import { Search, Filter, Plus, Refresh, Settings, Trash } from '@vicons/tabler'
import { useKiroAccountsStore } from '@/stores/kiroAccountsStore'

const { t } = useI18n()
const kiroStore = useKiroAccountsStore()

type KiroFilterStatus = 'all' | 'active' | 'banned' | 'warning'

type AddActionKey = 'kiro-sign' | 'json-import' | 'idc-builder' | 'idc-org'

const emit = defineEmits<{
  refresh: []
  'open-global-config': []
  'bulk-delete': []
  'add-kiro-sign': []
  'add-json': []
  'add-builder-idc': []
  'add-org-idc': []
}>()

const searchQuery = computed({
  get: () => kiroStore.searchQuery,
  set: (value: string) => kiroStore.setSearchQuery(value)
})

const filterStatus = computed({
  get: () => kiroStore.filterStatus,
  set: (value: KiroFilterStatus) => kiroStore.setFilterStatus(value)
})

const statusOptions = computed(() => [
  { label: t('kiro.allStatus'), value: 'all' },
  { label: t('kiro.status.active'), value: 'active' },
  { label: t('kiro.status.banned'), value: 'banned' },
  { label: t('kiro.status.warning'), value: 'warning' }
])

const addActionOptions = computed(() => [
  { label: t('kiro.kiroSignLogin'), key: 'kiro-sign' },
  { label: t('kiro.jsonImport'), key: 'json-import' },
  { label: 'AWS Builder ID', key: 'idc-builder' },
  { label: 'IAM Identity Center', key: 'idc-org' }
])

const accountCount = computed(() => kiroStore.accountCount)

function handleAddActionSelect(key: AddActionKey): void {
  if (key === 'kiro-sign') {
    emit('add-kiro-sign')
    return
  }
  if (key === 'json-import') {
    emit('add-json')
    return
  }
  if (key === 'idc-builder') {
    emit('add-builder-idc')
    return
  }
  emit('add-org-idc')
}
</script>

<template>
  <div class="kiro-account-toolbar">
    <div class="toolbar-left">
      <n-input
        v-model:value="searchQuery"
        :placeholder="t('kiro.searchPlaceholder')"
        clearable
        style="width: 260px"
      >
        <template #prefix>
          <n-icon><Search /></n-icon>
        </template>
      </n-input>

      <n-select
        v-model:value="filterStatus"
        :options="statusOptions"
        style="width: 150px"
      >
        <template #prefix>
          <n-icon><Filter /></n-icon>
        </template>
      </n-select>

      <n-space>
        <n-tag size="small" type="info">{{ t('kiro.total') }}: {{ accountCount.total }}</n-tag>
        <n-tag size="small" type="success">{{ t('kiro.active') }}: {{ accountCount.active }}</n-tag>
        <n-tag size="small" type="error">{{ t('kiro.banned') }}: {{ accountCount.banned }}</n-tag>
      </n-space>
    </div>

    <div class="toolbar-right">
      <n-button @click="emit('refresh')">
        <template #icon>
          <n-icon><Refresh /></n-icon>
        </template>
        {{ t('common.refresh') }}
      </n-button>

      <n-button @click="emit('open-global-config')">
        <template #icon>
          <n-icon><Settings /></n-icon>
        </template>
        {{ t('kiro.globalConfig') }}
      </n-button>

      <n-button type="error" ghost @click="emit('bulk-delete')">
        <template #icon>
          <n-icon><Trash /></n-icon>
        </template>
        {{ t('kiro.bulkDelete') }}
      </n-button>

      <n-dropdown
        trigger="click"
        :options="addActionOptions"
        @select="handleAddActionSelect"
      >
        <n-button type="primary">
          <template #icon>
            <n-icon><Plus /></n-icon>
          </template>
          {{ t('kiro.addAccount') }}
        </n-button>
      </n-dropdown>
    </div>
  </div>
</template>

<style scoped>
.kiro-account-toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 16px 20px;
  background: var(--bg-primary);
  border-bottom: 1px solid var(--border-color);
  gap: 16px;
  flex-wrap: wrap;
}

.toolbar-left {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

.toolbar-right {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

@media (max-width: 1024px) {
  .kiro-account-toolbar {
    flex-direction: column;
    align-items: stretch;
  }

  .toolbar-left,
  .toolbar-right {
    width: 100%;
  }
}
</style>
