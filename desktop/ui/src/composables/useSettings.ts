import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { endpointApi } from '@/api/endpoint'
import { useFeedback } from '@/composables/useFeedback'
import { getAvailableLanguages } from '@/i18n/vue-i18n'
import { useSettingsStore } from '@/stores/settingsStore'
import type { CLIConfigDirsPayload, SettingsPayload, WebDAVConfig } from '@/types/endpoint'

type LanguageOption = { label: string; value: string }
type LanguageSelectValue = string | number | boolean | null
type SaveGeneralSettingsOptions = { quiet?: boolean }

function toErrorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return String(error)
}

function normalizeSettings(settings: SettingsPayload): SettingsPayload {
  return {
    port: settings.port || 5600,
    apiKey: settings.apiKey || '',
    proxyUrl: settings.proxyUrl || '',
    fallback: !!settings.fallback,
    debugMode: settings.debugMode || '',
    listenAddr: settings.listenAddr || ''
  }
}

function normalizeWebdav(config: WebDAVConfig): WebDAVConfig {
  return {
    serverUrl: config.serverUrl || '',
    username: config.username || '',
    password: config.password || ''
  }
}

function resolveLanguageCode(lang: unknown, options: LanguageOption[]): string | null {
  if (typeof lang !== 'string') return null
  const raw = lang.trim()
  if (!raw) return null
  if (raw === 'zh') return 'zh-CN'
  if (raw === 'en' || raw === 'zh-CN') return raw

  const matchedByLabel = options.find((item) => item.label === raw)
  if (matchedByLabel) return matchedByLabel.value

  const lowered = raw.toLowerCase()
  if (lowered.startsWith('en')) return 'en'
  if (lowered.startsWith('zh')) return 'zh-CN'
  return null
}

export function useSettings() {
  const { t, locale } = useI18n()
  const feedback = useFeedback()
  const settingsStore = useSettingsStore()

  const settingsForm = ref<SettingsPayload>({
    port: 5600,
    apiKey: '',
    proxyUrl: '',
    fallback: false,
    debugMode: ''
  })

  const cliDirs = ref<CLIConfigDirsPayload>({
    claudeConfigDir: '',
    codexConfigDir: ''
  })

  const webdavForm = ref<WebDAVConfig>({
    serverUrl: '',
    username: '',
    password: ''
  })

  const languages = getAvailableLanguages() as Array<{ code: string; name: string }>
  const languageOptions = computed<LanguageOption[]>(() =>
    languages.map((lang) => ({ label: lang.name, value: lang.code }))
  )
  const debugModeOptions = computed<Array<{ label: string; value: string }>>(() => [
    { label: t('settings.debugModeNone'), value: '' },
    { label: t('settings.debugModeDb'), value: 'db' },
    { label: t('settings.debugModeFile'), value: 'file' }
  ])
  const currentLanguage = computed(() =>
    resolveLanguageCode(String(locale.value), languageOptions.value) ||
    resolveLanguageCode(settingsStore.language, languageOptions.value) ||
    'en'
  )

  function getWebDAVConfigFromForm(): WebDAVConfig {
    return {
      serverUrl: webdavForm.value.serverUrl.trim(),
      username: webdavForm.value.username.trim(),
      password: webdavForm.value.password
    }
  }

  async function loadGeneralData(): Promise<void> {
    const [settings, dirs] = await Promise.all([
      endpointApi.getSettings(),
      endpointApi.getCLIConfigDirs()
    ])

    settingsForm.value = normalizeSettings(settings)
    cliDirs.value = {
      claudeConfigDir: dirs.claudeConfigDir || '',
      codexConfigDir: dirs.codexConfigDir || ''
    }
  }

  async function loadWebDAVData(): Promise<void> {
    const webdav = await endpointApi.getWebDAVConfig()
    webdavForm.value = normalizeWebdav(webdav)
  }

  async function onLanguageSelect(lang: LanguageSelectValue): Promise<void> {
    const next = resolveLanguageCode(lang, languageOptions.value)
    const current = currentLanguage.value
    if (!next || next === current) return

    try {
      await settingsStore.changeLanguage(next)
    } catch (error) {
      feedback.error('Failed to change language: ' + toErrorMessage(error))
    }
  }

  async function refreshConfig(): Promise<boolean> {
    try {
      await settingsStore.refreshConfig()
      feedback.success(t('endpoints.refreshSuccess'))
      return true
    } catch (error) {
      feedback.error(t('endpoints.refreshFailed') + ': ' + toErrorMessage(error))
      return false
    }
  }

  async function saveGeneralSettings(options: SaveGeneralSettingsOptions = {}): Promise<boolean> {
    const quiet = options.quiet === true
    const port = Number(settingsForm.value.port)
    if (!Number.isInteger(port) || port < 1 || port > 65535) {
      if (!quiet) {
        feedback.error(t('settings.portHelp'))
      }
      return false
    }

    try {
      await endpointApi.saveSettings({
        ...settingsForm.value,
        proxyUrl: String(settingsForm.value.proxyUrl || '').trim(),
        port
      })
      await endpointApi.reloadConfig()

      await endpointApi.saveCLIConfigDirs({
        claudeConfigDir: cliDirs.value.claudeConfigDir.trim(),
        codexConfigDir: cliDirs.value.codexConfigDir.trim()
      })

      await settingsStore.loadSettings()
      if (!quiet) {
        feedback.success(t('settings.saveSuccess'))
      }
      return true
    } catch (error) {
      feedback.error(t('settings.saveFailed') + ': ' + toErrorMessage(error))
      return false
    }
  }

  async function testWebDAVConnection(): Promise<boolean> {
    const config = getWebDAVConfigFromForm()
    if (!config.serverUrl) {
      feedback.error(t('webdav.serverUrlRequired'))
      return false
    }

    try {
      const result = await endpointApi.testWebDAVConnection(config)
      if (result.error) {
        feedback.error(t('webdav.testFailed') + ': ' + result.error)
        return false
      }

      if (result.statusCode === 200 || result.statusCode === 207) {
        feedback.success(t('webdav.testSuccess'))
        return true
      }

      feedback.error(t('webdav.testFailed') + ': ' + String(result.statusCode))
      return false
    } catch (error) {
      feedback.error(t('webdav.testFailed') + ': ' + toErrorMessage(error))
      return false
    }
  }

  async function saveWebDAVSettings(): Promise<boolean> {
    const config = getWebDAVConfigFromForm()
    if (!config.serverUrl) {
      feedback.error(t('webdav.serverUrlRequired'))
      return false
    }

    try {
      await endpointApi.saveWebDAVConfig(config)
      webdavForm.value = config
      feedback.success(t('webdav.saveSuccess'))
      return true
    } catch (error) {
      feedback.error(t('webdav.saveFailed') + ': ' + toErrorMessage(error))
      return false
    }
  }

  return {
    settingsForm,
    cliDirs,
    webdavForm,
    languageOptions,
    debugModeOptions,
    currentLanguage,
    getWebDAVConfigFromForm,
    loadGeneralData,
    loadWebDAVData,
    onLanguageSelect,
    refreshConfig,
    saveGeneralSettings,
    testWebDAVConnection,
    saveWebDAVSettings
  }
}
