import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { codexApi } from '@/api/codex'
import type {
  CodexAccount,
  CodexAccountInput,
  CodexAccountStatus,
  CodexPagination,
  CodexTestResult,
  CodexUsageResult
} from '@/types/codex'

type CodexFilterStatus = 'all' | 'valid' | 'banned' | 'exhausted' | 'rate_limited' | 'cooling'

export const useCodexAccountsStore = defineStore('codexAccounts', () => {
  const accounts = ref<CodexAccount[]>([])
  const activeAccountId = ref<string | null>(null)
  const pagination = ref<CodexPagination>({
    offset: 0,
    limit: 20,
    nextOffset: 0,
    total: 0,
    hasMore: true
  })
  const loading = ref(false)
  const error = ref<string | null>(null)
  const searchQuery = ref('')
  const filterStatus = ref<CodexFilterStatus>('all')

  const filteredAccounts = computed(() => {
    let result = accounts.value

    if (searchQuery.value) {
      const query = searchQuery.value.toLowerCase()
      result = result.filter(
        (account) => account.email?.toLowerCase().includes(query)
      )
    }

    if (filterStatus.value !== 'all') {
      result = result.filter((account) => {
        if (filterStatus.value === 'cooling') {
          return Number(account.cooldownRemaining || 0) > 0
        }

        return account.status === filterStatus.value
      })
    }

    return result
  })

  const accountCount = computed(() => {
    const counts = {
      total: accounts.value.length,
      valid: 0,
      banned: 0,
      exhausted: 0,
      rate_limited: 0
    }

    accounts.value.forEach((account) => {
      if (account.status === 'valid') counts.valid += 1
      else if (account.status === 'banned' || account.status === 'reused') counts.banned += 1
      else if (account.status === 'exhausted') counts.exhausted += 1
      else if (account.status === 'rate_limited') counts.rate_limited += 1
    })

    return counts
  })

  function clearError(): void {
    error.value = null
  }

  async function loadAccounts(reset = true): Promise<void> {
    if (loading.value) return

    if (reset) {
      accounts.value = []
      pagination.value = {
        offset: 0,
        limit: 20,
        nextOffset: 0,
        total: 0,
        hasMore: true
      }
    } else if (!pagination.value.hasMore) {
      return
    }

    loading.value = true
    clearError()

    try {
      const page = await codexApi.getAccountsPage(pagination.value.nextOffset, pagination.value.limit)
      const pageAccounts = page.accounts || []

      if (reset) {
        accounts.value = pageAccounts
      } else {
        accounts.value = [...accounts.value, ...pageAccounts]
      }

      activeAccountId.value = page.activeAccountId || null
      pagination.value = {
        offset: page.offset,
        limit: page.limit,
        nextOffset: page.nextOffset,
        total: page.total,
        hasMore: page.hasMore
      }
    } catch (cause) {
      error.value = String(cause)
      throw cause
    } finally {
      loading.value = false
    }
  }

  async function loadMore(): Promise<void> {
    if (!pagination.value.hasMore || loading.value) return
    await loadAccounts(false)
  }

  async function setActiveAccount(accountId: string): Promise<void> {
    clearError()

    try {
      await codexApi.setActiveAccount(accountId)
      activeAccountId.value = accountId
      await loadAccounts(true)
    } catch (cause) {
      error.value = String(cause)
      throw cause
    }
  }

  async function addAccount(accountData: CodexAccountInput): Promise<void> {
    clearError()

    try {
      await codexApi.addAccount(accountData)
      await loadAccounts(true)
    } catch (cause) {
      error.value = String(cause)
      throw cause
    }
  }

  async function updateAccount(accountData: CodexAccountInput): Promise<void> {
    clearError()

    try {
      await codexApi.updateAccount(accountData)

      // Update the account in the local store without reloading
      const accountId = accountData.accountId
      if (accountId) {
        const index = accounts.value.findIndex(acc => acc.accountId === accountId)
        if (index !== -1) {
          // Use Object.assign to update in place, preserving reactivity without triggering full re-render
          Object.assign(accounts.value[index], accountData)
        }
      }
    } catch (cause) {
      error.value = String(cause)
      throw cause
    }
  }

  async function deleteAccount(accountId: string): Promise<void> {
    clearError()

    try {
      await codexApi.deleteAccount(accountId)
      await loadAccounts(true)
    } catch (cause) {
      error.value = String(cause)
      throw cause
    }
  }

  async function testAccount(accountId: string): Promise<CodexTestResult> {
    clearError()

    try {
      const result = await codexApi.testAccount(accountId)
      await loadAccounts(true)
      return result
    } catch (cause) {
      error.value = String(cause)
      throw cause
    }
  }

  async function fetchUsage(accountId: string): Promise<CodexUsageResult> {
    clearError()

    try {
      const result = await codexApi.getAccountUsage(accountId)
      await loadAccounts(true)
      return result
    } catch (cause) {
      error.value = String(cause)
      throw cause
    }
  }

  function setSearchQuery(query: string): void {
    searchQuery.value = query
  }

  function setFilterStatus(status: CodexFilterStatus): void {
    filterStatus.value = status
  }

  return {
    accounts,
    activeAccountId,
    pagination,
    loading,
    error,
    searchQuery,
    filterStatus,
    filteredAccounts,
    accountCount,
    loadAccounts,
    loadMore,
    setActiveAccount,
    addAccount,
    updateAccount,
    deleteAccount,
    testAccount,
    fetchUsage,
    setSearchQuery,
    setFilterStatus,
    clearError
  }
})
