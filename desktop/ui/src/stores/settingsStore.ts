import { defineStore } from 'pinia'
import { ref } from 'vue'
import { endpointApi } from '@/api/endpoint'
import { setLanguage } from '@/i18n/vue-i18n'
import type { SettingsPayload } from '@/types/endpoint'

function normalizeSettings(settings: SettingsPayload): SettingsPayload {
  return {
    port: settings.port || 5600,
    apiKey: settings.apiKey || '',
    fallback: !!settings.fallback,
    clashPath: settings.clashPath || '',
    debugMode: settings.debugMode || '',
    listenAddr: settings.listenAddr || ''
  }
}

export const useSettingsStore = defineStore('settings', () => {
  const language = ref('en')
  const settings = ref<SettingsPayload>({
    port: 5600,
    apiKey: '',
    clashPath: '',
    fallback: false,
    debugMode: '',
    listenAddr: ''
  })

  async function loadLanguage(): Promise<void> {
    try {
      if (window.go?.main?.App?.GetLanguage) {
        const lang = await window.go.main.App.GetLanguage()
        if (lang) {
          language.value = lang
          setLanguage(lang)
        }
      }
    } catch (error) {
      console.error('Failed to load language:', error)
    }
  }

  async function changeLanguage(lang: string): Promise<void> {
    if (window.go?.main?.App?.SetLanguage) {
      await window.go.main.App.SetLanguage(lang)
    }

    language.value = lang
    setLanguage(lang)

    window.dispatchEvent(new Event('home:endpoints-updated'))
    window.dispatchEvent(new Event('home:logs-updated'))
  }

  async function loadSettings(): Promise<void> {
    try {
      settings.value = normalizeSettings(await endpointApi.getSettings())
    } catch (error) {
      console.error('Failed to load settings:', error)
    }
  }

  async function refreshConfig(): Promise<void> {
    await endpointApi.reloadConfig()
    await loadSettings()
    window.dispatchEvent(new Event('home:endpoints-updated'))
  }

  return {
    language,
    settings,
    loadLanguage,
    changeLanguage,
    loadSettings,
    refreshConfig
  }
})
