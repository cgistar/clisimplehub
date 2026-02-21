import { ref } from 'vue'
import { defineStore } from 'pinia'
import { endpointApi } from '@/api/endpoint'
import type { Vendor, VendorInput } from '@/types/endpoint'

function toErrorMessage(cause: unknown): string {
  if (cause instanceof Error) return cause.message
  return String(cause)
}

export const useVendorsStore = defineStore('vendors', () => {
  const vendors = ref<Vendor[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)

  function clearError(): void {
    error.value = null
  }

  async function loadVendors(): Promise<void> {
    loading.value = true
    clearError()
    try {
      vendors.value = (await endpointApi.getVendors()) || []
    } catch (cause) {
      error.value = toErrorMessage(cause)
      throw cause
    } finally {
      loading.value = false
    }
  }

  async function saveVendor(input: VendorInput): Promise<Vendor> {
    clearError()
    try {
      const saved = await endpointApi.saveVendor(input)
      await loadVendors()
      return saved
    } catch (cause) {
      error.value = toErrorMessage(cause)
      throw cause
    }
  }

  async function deleteVendorById(vendorId: number): Promise<void> {
    clearError()
    try {
      await endpointApi.deleteVendor(vendorId)
      await loadVendors()
    } catch (cause) {
      error.value = toErrorMessage(cause)
      throw cause
    }
  }

  return {
    vendors,
    loading,
    error,
    loadVendors,
    saveVendor,
    deleteVendorById,
    clearError
  }
})
