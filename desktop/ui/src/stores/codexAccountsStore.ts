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

  function patchAccountById(accountId: string, patch: Partial<CodexAccount>): boolean {
    const normalizedId = String(accountId || '').trim()
    if (!normalizedId) return false

    const index = accounts.value.findIndex((account) => String(account.id || '').trim() === normalizedId)
    if (index === -1) return false

    accounts.value[index] = {
      ...accounts.value[index],
      ...patch
    }

    return true
  }

  function toSafeNumber(value: unknown): number {
    const num = Number(value)
    return Number.isFinite(num) ? num : 0
  }

  function normalizeUsage(result: CodexUsageResult): CodexAccount['codexUsage'] {
    const primary = result?.primary
      ? {
          usedPercent: toSafeNumber(result.primary.usedPercent),
          remainingSeconds: toSafeNumber(result.primary.remainingSeconds)
        }
      : undefined

    const secondary = result?.secondary
      ? {
          usedPercent: toSafeNumber(result.secondary.usedPercent),
          remainingSeconds: toSafeNumber(result.secondary.remainingSeconds)
        }
      : undefined

    if (!primary && !secondary) return undefined

    return {
      primary,
      secondary
    }
  }

  async function syncVisibleAccountById(accountId: string): Promise<boolean> {
    const normalizedId = String(accountId || '').trim()
    if (!normalizedId) return false

    const loadedCount = Math.max(accounts.value.length, pagination.value.nextOffset, pagination.value.limit)
    if (loadedCount <= 0) return false

    const page = await codexApi.getAccountsPage(0, loadedCount)
    const latest = (page.accounts || []).find(
      (account) => String(account.id || '').trim() === normalizedId
    )

    if (page.activeAccountId) {
      activeAccountId.value = page.activeAccountId
    }

    if (!latest) return false
    return patchAccountById(normalizedId, latest)
  }

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
      total: pagination.value.total,
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

      const accountId = accountData.id
      if (accountId) {
        patchAccountById(accountId, accountData)
      }
    } catch (cause) {
      error.value = String(cause)
      throw cause
    }
  }

  async function restoreAccount(accountId: string): Promise<void> {
    clearError()

    try {
      await codexApi.restoreAccount(accountId)
      patchAccountById(accountId, {
        status: 'valid',
        cooldownUntil: '',
        cooldownReason: '',
        cooldownRemaining: 0
      })
    } catch (cause) {
      error.value = String(cause)
      throw cause
    }
  }

  async function deleteAccount(accountId: string, options?: { reload?: boolean }): Promise<void> {
    clearError()

    try {
      await codexApi.deleteAccount(accountId)
      if (options?.reload === false) {
        return
      }
      await loadAccounts(true)
    } catch (cause) {
      error.value = String(cause)
      throw cause
    }
  }

  async function deleteAccounts(accountIds: string[], options?: { reload?: boolean }): Promise<void> {
    clearError()

    try {
      await codexApi.deleteAccounts(accountIds)
      if (options?.reload === false) {
        return
      }
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

      let synced = false
      try {
        synced = await syncVisibleAccountById(accountId)
      } catch {
        synced = false
      }

      if (!synced) {
        patchAccountById(accountId, {
          accountId: result.accountId,
          accessToken: result.accessToken,
          email: result.email,
          planType: result.planType,
          expiresAt: result.expiresAt,
          status: 'valid',
          cooldownUntil: '',
          cooldownReason: '',
          cooldownRemaining: 0
        })
      }

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
      patchAccountById(accountId, {
        codexUsage: normalizeUsage(result)
      })
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
    restoreAccount,
    deleteAccount,
    deleteAccounts,
    testAccount,
    fetchUsage,
    setSearchQuery,
    setFilterStatus,
    clearError
  }
})
