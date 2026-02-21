import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { kiroApi } from '@/api/kiro'
import type {
  KiroAccount,
  KiroAccountInput,
  KiroAccountsResponse,
  KiroAccountStatus,
  KiroTestResult,
  KiroUsageResult
} from '@/types/kiro'

type KiroFilterStatus = 'all' | 'active' | 'banned' | 'warning'

export const useKiroAccountsStore = defineStore('kiroAccounts', () => {
  const accounts = ref<KiroAccount[]>([])
  const activeRefreshToken = ref<string | null>(null)
  const loading = ref(false)
  const error = ref<string | null>(null)
  const searchQuery = ref('')
  const filterStatus = ref<KiroFilterStatus>('all')

  const filteredAccounts = computed(() => {
    let result = accounts.value

    if (searchQuery.value) {
      const query = searchQuery.value.toLowerCase()
      result = result.filter((account) => account.email?.toLowerCase().includes(query))
    }

    if (filterStatus.value !== 'all') {
      result = result.filter((account) => account.status === filterStatus.value)
    }

    return result
  })

  const activeAccount = computed(() => {
    return accounts.value.find((account) => account.refreshToken === activeRefreshToken.value)
  })

  const accountCount = computed(() => ({
    total: accounts.value.length,
    active: accounts.value.filter((account) => account.status === 'active').length,
    banned: accounts.value.filter((account) => account.status === 'banned').length,
    warning: accounts.value.filter((account) => account.status === 'warning').length
  }))

  function clearError(): void {
    error.value = null
  }

  async function loadAccounts(): Promise<void> {
    loading.value = true
    clearError()

    try {
      const result: KiroAccountsResponse = await kiroApi.getAccounts()
      accounts.value = result.accounts || []
      activeRefreshToken.value = result.activeRefreshToken || null
    } catch (cause) {
      error.value = String(cause)
      throw cause
    } finally {
      loading.value = false
    }
  }

  async function setActiveAccount(refreshToken: string): Promise<void> {
    clearError()
    try {
      await kiroApi.setActiveAccount(refreshToken)
      activeRefreshToken.value = refreshToken
      await loadAccounts()
    } catch (cause) {
      error.value = String(cause)
      throw cause
    }
  }

  async function addAccount(accountData: KiroAccountInput): Promise<void> {
    clearError()
    try {
      await kiroApi.addAccount(accountData)
      await loadAccounts()
    } catch (cause) {
      error.value = String(cause)
      throw cause
    }
  }

  async function updateAccount(refreshToken: string, accountData: KiroAccountInput): Promise<void> {
    clearError()
    try {
      await kiroApi.updateAccount(refreshToken, accountData)
      await loadAccounts()
    } catch (cause) {
      error.value = String(cause)
      throw cause
    }
  }

  async function deleteAccount(refreshToken: string): Promise<void> {
    clearError()
    try {
      await kiroApi.deleteAccount(refreshToken)
      await loadAccounts()
    } catch (cause) {
      error.value = String(cause)
      throw cause
    }
  }

  async function testAccount(refreshToken: string): Promise<KiroTestResult> {
    clearError()
    try {
      return await kiroApi.testAccount(refreshToken)
    } catch (cause) {
      error.value = String(cause)
      throw cause
    }
  }

  async function fetchUsage(refreshToken: string): Promise<KiroUsageResult> {
    clearError()
    try {
      return await kiroApi.getAccountUsage(refreshToken)
    } catch (cause) {
      error.value = String(cause)
      throw cause
    }
  }

  function setSearchQuery(query: string): void {
    searchQuery.value = query
  }

  function setFilterStatus(status: KiroFilterStatus): void {
    filterStatus.value = status
  }

  return {
    accounts,
    activeRefreshToken,
    loading,
    error,
    searchQuery,
    filterStatus,
    filteredAccounts,
    activeAccount,
    accountCount,
    loadAccounts,
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
