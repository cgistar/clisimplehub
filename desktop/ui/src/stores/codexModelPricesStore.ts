import { ref } from 'vue'
import { defineStore } from 'pinia'
import { codexApi } from '@/api/codex'
import type { CodexModelPrice } from '@/types/codex'

export const useCodexModelPricesStore = defineStore('codexModelPrices', () => {
  const prices = ref<CodexModelPrice[]>([])
  const loaded = ref(false)
  const loading = ref(false)
  let loadingPromise: Promise<CodexModelPrice[]> | null = null

  async function loadPrices(): Promise<CodexModelPrice[]> {
    if (loaded.value) return prices.value
    if (loadingPromise) return loadingPromise

    loading.value = true
    loadingPromise = codexApi.getModelPrices()
      .then((result) => {
        prices.value = result
        loaded.value = true
        return prices.value
      })
      .finally(() => {
        loading.value = false
        loadingPromise = null
      })
    return loadingPromise
  }

  async function savePrices(nextPrices: CodexModelPrice[]): Promise<CodexModelPrice[]> {
    const saved = await codexApi.saveModelPrices(nextPrices)
    prices.value = saved
    loaded.value = true
    return saved
  }

  return { prices, loaded, loading, loadPrices, savePrices }
})
