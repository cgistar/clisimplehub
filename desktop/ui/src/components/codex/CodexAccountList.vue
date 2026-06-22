<template>
  <div class="codex-account-list">
    <div v-if="filteredAccounts.length === 0 && !loading" class="no-accounts">
      <p>{{ t('codex.noAccounts') }}</p>
    </div>

    <div v-else ref="scrollRef" class="account-grid" @scroll="handleScroll">
      <CodexAccountCard
        v-for="item in filteredAccounts"
        :key="item.id"
        :account="item"
        :is-active="item.id === activeAccountId"
        :busy="isAccountPending(item.id)"
        @activate="handleActivate"
        @test="handleTest"
        @fetch-usage="handleFetchUsage"
        @fetch-primary-usage="handleFetchPrimaryUsage"
        @reset-credit="handleResetCredit"
        @copy="handleCopy"
        @get-token="(account: CodexAccount) => emit('get-token', account)"
        @edit="handleEdit"
        @delete="handleDelete"
      />
    </div>

    <div v-if="loading && hasMore" class="loading-indicator">
      <n-spin size="small" />
      <span>{{ t('codex.loadingMore') }}</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, nextTick } from 'vue'
import { NSpin, useMessage } from 'naive-ui'
import { useI18n } from 'vue-i18n'
import { storeToRefs } from 'pinia'
import { useCodexAccountsStore } from '../../stores/codexAccountsStore'
import type { CodexAccount } from '@/types/codex'
import { buildCodexAccountCopyData } from '@/utils/codexAccountCopy'
import CodexAccountCard from './CodexAccountCard.vue'

const { t } = useI18n()
const message = useMessage()
const codexStore = useCodexAccountsStore()

const {
  filteredAccounts,
  activeAccountId,
  loading,
  pagination
} = storeToRefs(codexStore)

const scrollRef = ref<HTMLElement | null>(null)
const pendingAccountIds = ref<Set<string>>(new Set())
let savedScrollPosition = 0

const emit = defineEmits<{
  edit: [account: CodexAccount]
  'get-token': [account: CodexAccount]
}>()

const hasMore = computed(() => pagination.value.hasMore)
const total = computed(() => pagination.value.total)

function toErrorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return String(error)
}

function isAccountPending(accountId: string): boolean {
  return pendingAccountIds.value.has(accountId)
}

function setAccountPending(accountId: string, pending: boolean): void {
  const next = new Set(pendingAccountIds.value)
  if (pending) {
    next.add(accountId)
  } else {
    next.delete(accountId)
  }
  pendingAccountIds.value = next
}

async function runWithAccountPending(accountId: string, task: () => Promise<void>): Promise<void> {
  if (isAccountPending(accountId)) return

  setAccountPending(accountId, true)
  try {
    await task()
  } finally {
    setAccountPending(accountId, false)
  }
}

function saveScrollPosition(): void {
  if (scrollRef.value) {
    savedScrollPosition = scrollRef.value.scrollTop
  }
}

function restoreScrollPosition(): void {
  if (scrollRef.value && savedScrollPosition > 0) {
    nextTick(() => {
      if (scrollRef.value) {
        scrollRef.value.scrollTop = savedScrollPosition
      }
    })
  }
}

function handleScroll(event: Event): void {
  const target = event.target as HTMLElement | null
  if (!target) return

  const { scrollTop, scrollHeight, clientHeight } = target
  if (scrollHeight - scrollTop - clientHeight < 200) {
    codexStore.loadMore()
  }
}

async function handleActivate(accountId: string): Promise<void> {
  await runWithAccountPending(accountId, async () => {
    try {
      saveScrollPosition()
      await codexStore.setActiveAccount(accountId)
      message.success(t('codex.accountSwitched'))
      restoreScrollPosition()
    } catch (error) {
      message.error(t('codex.switchAccountFailed') + ': ' + toErrorMessage(error))
    }
  })
}

async function handleTest(accountId: string): Promise<void> {
  await runWithAccountPending(accountId, async () => {
    try {
      saveScrollPosition()
      await codexStore.testAccount(accountId)
      message.success(t('codex.testSuccess'))
      restoreScrollPosition()
    } catch (error) {
      message.error(t('codex.testFailed') + ': ' + toErrorMessage(error))
    }
  })
}

async function handleFetchUsage(accountId: string): Promise<void> {
  await runWithAccountPending(accountId, async () => {
    try {
      saveScrollPosition()
      await codexStore.fetchUsage(accountId)
      message.success(t('codex.usageSuccess'))
      restoreScrollPosition()
    } catch (error) {
      message.error(t('codex.usageFailedPrefix') + ': ' + toErrorMessage(error))
    }
  })
}

async function handleFetchPrimaryUsage(accountId: string): Promise<void> {
  await runWithAccountPending(accountId, async () => {
    try {
      saveScrollPosition()
      await codexStore.fetchPrimaryUsage(accountId)
      message.success(t('codex.usageSuccess'))
      restoreScrollPosition()
    } catch (error) {
      message.error(t('codex.usageFailedPrefix') + ': ' + toErrorMessage(error))
    }
  })
}

async function handleResetCredit(accountId: string): Promise<void> {
  await runWithAccountPending(accountId, async () => {
    try {
      saveScrollPosition()
      const result = await codexStore.consumeResetCredit(accountId)
      message.success(t('codex.resetRateLimitSuccess', { count: result.windows_reset || 0 }))
      restoreScrollPosition()
    } catch (error) {
      message.error(t('codex.resetRateLimitFailed') + ': ' + toErrorMessage(error))
    }
  })
}

async function handleCopy(account: CodexAccount): Promise<void> {
  try {
    await navigator.clipboard.writeText(JSON.stringify(buildCodexAccountCopyData(account), null, 2))
    message.success(t('codex.copySuccess'))
  } catch (error) {
    message.error(t('codex.copyFailed') + ': ' + toErrorMessage(error))
  }
}

function handleEdit(account: CodexAccount): void {
  saveScrollPosition()
  emit('edit', account)
}

async function handleDelete(accountId: string): Promise<void> {
  await runWithAccountPending(accountId, async () => {
    try {
      saveScrollPosition()
      await codexStore.deleteAccount(accountId)
      message.success(t('codex.accountDeleted'))
      restoreScrollPosition()
    } catch (error) {
      message.error(t('codex.deleteAccountFailed') + ': ' + toErrorMessage(error))
    }
  })
}

// Expose restore function for parent component
defineExpose({
  restoreScrollPosition
})
</script>

<style scoped>
.codex-account-list {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.account-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, 350px);
  gap: 20px;
  padding: 8px 0;
  flex: 1;
  overflow-y: auto;
  padding-right: 4px;
  justify-content: start;
  align-content: start;
}

.no-accounts {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 400px;
  color: var(--text-tertiary);
  font-size: 14px;
}

.loading-indicator {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 16px;
  color: var(--text-secondary);
  font-size: 13px;
}

.load-done {
  text-align: center;
  padding: 16px;
  color: var(--text-tertiary);
  font-size: 12px;
}

@media (max-width: 760px) {
  .account-grid {
    grid-template-columns: 1fr;
  }
}
</style>
