<template>
  <div class="xai-account-list">
    <div v-if="filteredAccounts.length === 0 && !loading" class="no-accounts">
      <p>{{ t('xai.noAccounts') }}</p>
    </div>

    <div v-else ref="scrollRef" class="account-grid" @scroll="handleScroll">
      <XaiAccountCard
        v-for="item in filteredAccounts"
        :key="item.id"
        :account="item"
        :is-active="item.id === activeAccountId"
        :busy="isAccountPending(item.id || '')"
        @activate="handleActivate"
        @test="handleTest"
        @copy="handleCopy"
        @edit="handleEdit"
        @delete="handleDelete"
      />
    </div>

    <div v-if="loading && hasMore" class="loading-indicator">
      <n-spin size="small" />
      <span>{{ t('xai.loadingMore') }}</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { NSpin, useMessage } from 'naive-ui'
import { useI18n } from 'vue-i18n'
import { storeToRefs } from 'pinia'
import { useXaiAccountsStore } from '../../stores/xaiAccountsStore'
import type { XaiAccount } from '@/types/xai'
import { buildXaiAccountCopyData } from '@/utils/xaiAccountCopy'
import XaiAccountCard from './XaiAccountCard.vue'

const { t } = useI18n()
const message = useMessage()
const xaiStore = useXaiAccountsStore()

const {
  filteredAccounts,
  activeAccountId,
  loading,
  pagination
} = storeToRefs(xaiStore)

const scrollRef = ref<HTMLElement | null>(null)
const pendingAccountIds = ref<Set<string>>(new Set())

const emit = defineEmits<{
  edit: [account: XaiAccount]
}>()

const hasMore = computed(() => pagination.value.hasMore)

function toErrorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return String(error)
}

function isAccountPending(accountId: string): boolean {
  return pendingAccountIds.value.has(accountId)
}

function setAccountPending(accountId: string, pending: boolean): void {
  const next = new Set(pendingAccountIds.value)
  if (pending) next.add(accountId)
  else next.delete(accountId)
  pendingAccountIds.value = next
}

async function handleActivate(accountId: string): Promise<void> {
  if (!accountId || isAccountPending(accountId)) return
  setAccountPending(accountId, true)
  try {
    await xaiStore.setActiveAccount(accountId)
    message.success(t('xai.accountSwitched'))
  } catch (error) {
    message.error(t('xai.switchAccountFailed') + toErrorMessage(error))
  } finally {
    setAccountPending(accountId, false)
  }
}

async function handleTest(accountId: string): Promise<void> {
  if (!accountId || isAccountPending(accountId)) return
  setAccountPending(accountId, true)
  try {
    await xaiStore.testAccount(accountId)
    message.success(t('xai.testSuccess'))
  } catch (error) {
    message.error(t('xai.testFailed') + toErrorMessage(error))
  } finally {
    setAccountPending(accountId, false)
  }
}

async function handleCopy(account: XaiAccount): Promise<void> {
  try {
    await navigator.clipboard.writeText(JSON.stringify(buildXaiAccountCopyData(account), null, 2))
    message.success(t('xai.copySuccess'))
  } catch (error) {
    message.error(t('xai.copyFailed') + toErrorMessage(error))
  }
}

function handleEdit(account: XaiAccount): void {
  emit('edit', account)
}

async function handleDelete(accountId: string): Promise<void> {
  if (!accountId || isAccountPending(accountId)) return
  setAccountPending(accountId, true)
  try {
    await xaiStore.deleteAccount(accountId)
    message.success(t('xai.accountDeleted'))
  } catch (error) {
    message.error(t('xai.deleteAccountFailed') + toErrorMessage(error))
  } finally {
    setAccountPending(accountId, false)
  }
}

async function handleScroll(event: Event): Promise<void> {
  const el = event.target as HTMLElement
  if (!el || !hasMore.value || loading.value) return
  if (el.scrollTop + el.clientHeight >= el.scrollHeight - 80) {
    try {
      await xaiStore.loadMore()
    } catch {
      // ignore
    }
  }
}

defineExpose({
  reload: () => xaiStore.loadAccounts(true)
})
</script>

<style scoped>
.xai-account-list {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.no-accounts {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--text-tertiary);
  font-size: 14px;
}

.account-grid {
  flex: 1;
  min-height: 0;
  overflow: auto;
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: 12px;
  align-content: start;
  padding-bottom: 8px;
}

.loading-indicator {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 8px;
  color: var(--text-secondary);
  font-size: 12px;
}
</style>
