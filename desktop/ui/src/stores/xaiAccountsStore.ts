import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { xaiApi } from '@/api/xai'
import type { XaiAccount, XaiAccountInput, XaiAccountStatus } from '@/types/xai'

type XaiFilterStatus = 'all' | 'valid' | 'banned' | 'exhausted' | 'cooling'

export const useXaiAccountsStore = defineStore('xaiAccounts', () => {
  const accounts = ref<XaiAccount[]>([])
  const activeAccountId = ref<string | null>(null)
  const pagination = ref({
    offset: 0,
    limit: 50,
    nextOffset: 0,
    total: 0,
    hasMore: true
  })
  const loading = ref(false)
  const error = ref<string | null>(null)
  const searchQuery = ref('')
  const filterStatus = ref<XaiFilterStatus>('all')

  function patchAccountById(accountId: string, patch: Partial<XaiAccount>): boolean {
    const normalizedId = String(accountId || '').trim()
    if (!normalizedId) return false
    const index = accounts.value.findIndex((account) => String(account.id || '').trim() === normalizedId)
    if (index === -1) return false
    accounts.value[index] = { ...accounts.value[index], ...patch }
    return true
  }

  const filteredAccounts = computed(() => {
    let result = accounts.value
    if (searchQuery.value) {
      const query = searchQuery.value.toLowerCase()
      result = result.filter(
        (account) =>
          account.email?.toLowerCase().includes(query) ||
          account.subject?.toLowerCase().includes(query) ||
          account.id?.toLowerCase().includes(query)
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
      exhausted: 0
    }
    for (const account of accounts.value) {
      const status = (account.status || 'valid') as XaiAccountStatus
      if (status === 'banned') counts.banned += 1
      else if (status === 'exhausted') counts.exhausted += 1
      else counts.valid += 1
    }
    return counts
  })

  async function loadAccounts(reset = true): Promise<void> {
    if (loading.value) return
    loading.value = true
    error.value = null
    try {
      const offset = reset ? 0 : pagination.value.nextOffset
      const page = await xaiApi.getAccountsPage(offset, pagination.value.limit)
      const nextAccounts = page.accounts || []
      accounts.value = reset ? nextAccounts : [...accounts.value, ...nextAccounts]
      activeAccountId.value = page.activeAccountId || null
      pagination.value = {
        offset: page.offset,
        limit: page.limit,
        nextOffset: page.nextOffset,
        total: page.total,
        hasMore: page.hasMore
      }
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e)
      throw e
    } finally {
      loading.value = false
    }
  }

  async function loadMore(): Promise<void> {
    if (!pagination.value.hasMore || loading.value) return
    await loadAccounts(false)
  }

  async function setActiveAccount(accountId: string): Promise<void> {
    await xaiApi.setActiveAccount(accountId)
    activeAccountId.value = accountId
    accounts.value = accounts.value.map((account) => ({
      ...account,
      isActive: String(account.id || '') === String(accountId)
    }))
  }

  async function addAccount(input: XaiAccountInput): Promise<XaiAccount> {
    const account = await xaiApi.addAccount(input)
    await loadAccounts(true)
    return account
  }

  async function updateAccount(input: XaiAccountInput): Promise<void> {
    await xaiApi.updateAccount(input)
    if (input.id) {
      patchAccountById(input.id, input)
    }
    await loadAccounts(true)
  }

  async function deleteAccount(accountId: string): Promise<void> {
    await xaiApi.deleteAccount(accountId)
    accounts.value = accounts.value.filter((account) => String(account.id || '') !== String(accountId))
    pagination.value.total = Math.max(0, pagination.value.total - 1)
    if (activeAccountId.value === accountId) {
      activeAccountId.value = accounts.value[0]?.id || null
    }
  }

  async function deleteAccounts(accountIds: string[]): Promise<void> {
    await xaiApi.deleteAccounts(accountIds)
    const idSet = new Set(accountIds.map((id) => String(id)))
    accounts.value = accounts.value.filter((account) => !idSet.has(String(account.id || '')))
    await loadAccounts(true)
  }

  async function testAccount(accountId: string): Promise<void> {
    const result = await xaiApi.testAccount(accountId)
    if (!result.success) {
      throw new Error(result.error || 'test failed')
    }
    if (result.account?.id) {
      patchAccountById(result.account.id, result.account)
    } else {
      await loadAccounts(true)
    }
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
    deleteAccounts,
    testAccount
  }
})
