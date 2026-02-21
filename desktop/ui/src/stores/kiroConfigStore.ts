import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { kiroApi } from '@/api/kiro'
import { t } from '@/i18n/vue-i18n'
import type { KiroAuthCredential, KiroConfig, KiroGlobalConfig, KiroTestResult, KiroUsageResult } from '@/types/kiro'

function defaultKiroConfig(): KiroConfig {
  return {
    refreshToken: '',
    profileArn: '',
    region: 'us-east-1',
    proxyUrl: '',
    userAgent: '',
    version: '',
    bufferedStream: false,
    authMethod: 'social',
    provider: '',
    clientId: '',
    clientSecret: '',
    accessToken: '',
    expiresAt: ''
  }
}

function defaultKiroGlobalConfig(): KiroGlobalConfig {
  return {
    region: 'us-east-1',
    proxyUrl: '',
    userAgent: '',
    version: '',
    bufferedStream: false,
    rotationMode: 'fixed',
    modelMapping: {}
  }
}

function toErrorMessage(error: unknown): string {
  if (error instanceof Error && error.message) return error.message
  return String(error)
}

function normalizeErrorMessage(error: unknown): string {
  if (error === undefined || error === null) return ''
  if (typeof error === 'string') return error
  if (typeof (error as { message?: unknown })?.message === 'string') return (error as { message: string }).message
  try {
    return JSON.stringify(error)
  } catch {
    return String(error)
  }
}

function parseKiroUsageHttpError(error: unknown): Record<string, unknown> | null {
  const message = normalizeErrorMessage(error)
  const marker = 'KIRO_USAGE_HTTP_ERROR:'
  const idx = message.indexOf(marker)
  if (idx < 0) return null

  const jsonText = message.slice(idx + marker.length).trim()
  if (!jsonText) return null

  try {
    const parsed = JSON.parse(jsonText)
    if (!parsed || typeof parsed !== 'object') return null
    return parsed as Record<string, unknown>
  } catch {
    return null
  }
}

function formatKiroUsageHttpError(detail: Record<string, unknown>): string {
  const rawCode = Number(detail.statusCode)
  const hasStatusCode = Number.isFinite(rawCode) && rawCode > 0
  const statusText = hasStatusCode ? `HTTP ${rawCode}` : 'HTTP error'

  const hint = String(detail.hint || '').trim()
  const body = String(detail.body || '').trim()

  let msg = hint || statusText
  if (hint && hasStatusCode) msg += ` (${statusText})`
  if (!hint && hasStatusCode) msg = statusText
  if (body) msg += `: ${body}`

  return msg
}

function extractRegionFromProfileArn(profileArn: string | undefined): string {
  const arn = String(profileArn || '').trim()
  if (!arn) return ''

  const parts = arn.split(':')
  if (parts.length < 6 || parts[0] !== 'arn') return ''

  const region = (parts[3] || '').trim()
  if (!/^[a-z]{2}-[a-z0-9-]+-\d+$/.test(region)) return ''
  return region
}

function formatUsageNumber(value: unknown): string {
  const num = Number(value)
  if (!Number.isFinite(num)) return '0'
  return num.toLocaleString(undefined, { maximumFractionDigits: 4 })
}

function formatUsagePct(value: unknown): string {
  const num = Number(value)
  if (!Number.isFinite(num)) return ''
  return `${num.toFixed(1)}%`
}

export const useKiroConfigStore = defineStore('kiroConfig', () => {
  const config = ref<KiroConfig>(defaultKiroConfig())
  const globalConfig = ref<KiroGlobalConfig>(defaultKiroGlobalConfig())
  const usageInfo = ref('')

  const loadingConfig = ref(false)
  const testingConfig = ref(false)
  const savingConfig = ref(false)
  const loadingUsage = ref(false)
  const loadingGlobalConfig = ref(false)
  const savingGlobalConfig = ref(false)

  const initialRefreshToken = ref('')
  const testedRefreshToken = ref('')
  const testedCredential = ref<KiroAuthCredential | null>(null)
  const loadedProvider = ref('')

  const refreshTokenChanged = computed(() => (config.value.refreshToken || '').trim() !== initialRefreshToken.value)
  const canSaveConfig = computed(() => {
    const refreshToken = (config.value.refreshToken || '').trim()
    if (!refreshToken) return false

    if (String(config.value.authMethod || 'social').toLowerCase() === 'idc') {
      if (!String(config.value.clientId || '').trim() || !String(config.value.clientSecret || '').trim()) {
        return false
      }
    }

    if (!refreshTokenChanged.value) return true
    return testedRefreshToken.value === refreshToken && !!testedCredential.value?.accessToken
  })

  const canFetchUsage = computed(() => !!String(config.value.accessToken || '').trim())

  function markRefreshTokenInput(): void {
    const current = String(config.value.refreshToken || '').trim()
    if (current !== testedRefreshToken.value) {
      testedRefreshToken.value = ''
      testedCredential.value = null
      usageInfo.value = ''
      config.value.accessToken = ''
      config.value.profileArn = ''
      config.value.expiresAt = ''
    }
  }

  function markIdcFieldInput(): void {
    const current = String(config.value.refreshToken || '').trim()
    if (current === testedRefreshToken.value) {
      testedRefreshToken.value = ''
      testedCredential.value = null
      usageInfo.value = ''
      config.value.accessToken = ''
      config.value.profileArn = ''
      config.value.expiresAt = ''
    }
  }

  async function loadConfig(): Promise<void> {
    loadingConfig.value = true
    try {
      const result = await kiroApi.getConfig()
      config.value = {
        ...defaultKiroConfig(),
        ...result,
        region: result?.region || 'us-east-1',
        authMethod: result?.authMethod || 'social'
      }

      initialRefreshToken.value = String(config.value.refreshToken || '').trim()
      testedRefreshToken.value = ''
      testedCredential.value = null
      usageInfo.value = ''
      loadedProvider.value = result?.provider || ''
    } finally {
      loadingConfig.value = false
    }
  }

  function applyAuthCredential(credential: KiroAuthCredential): void {
    if (!credential) return

    const nextRefreshToken = String(credential.refreshToken || '').trim()
    config.value.refreshToken = nextRefreshToken
    config.value.accessToken = credential.accessToken || ''
    config.value.profileArn = credential.profileArn || ''
    config.value.expiresAt = credential.expiresAt || ''
    config.value.region = credential.region || config.value.region || 'us-east-1'
    config.value.authMethod = credential.authMethod || config.value.authMethod
    config.value.provider = credential.provider || config.value.provider || ''

    if (credential.authMethod === 'idc') {
      config.value.clientId = credential.clientId || config.value.clientId || ''
      config.value.clientSecret = credential.clientSecret || config.value.clientSecret || ''
    }

    testedRefreshToken.value = nextRefreshToken
    testedCredential.value = {
      ...credential,
      refreshToken: nextRefreshToken,
      accessToken: credential.accessToken || ''
    }
    usageInfo.value = ''
  }

  async function testCurrentConfig(): Promise<KiroTestResult> {
    const refreshToken = String(config.value.refreshToken || '').trim()
    if (!refreshToken) {
      throw new Error(t('kiro.refreshTokenRequired'))
    }

    if (String(config.value.authMethod || 'social').toLowerCase() === 'idc') {
      if (!String(config.value.clientId || '').trim() || !String(config.value.clientSecret || '').trim()) {
        throw new Error(t('kiro.idcFieldsRequired'))
      }
    }

    testingConfig.value = true
    try {
      const result = await kiroApi.testRefreshToken({ ...config.value, refreshToken })

      if (!result?.accessToken) {
        throw new Error('Empty accessToken')
      }

      const nextRefreshToken = String(result.refreshToken || refreshToken).trim()
      config.value.refreshToken = nextRefreshToken
      config.value.accessToken = result.accessToken || ''
      config.value.profileArn = result.profileArn || ''
      config.value.expiresAt = result.expiresAt || ''

      const regionFromArn = extractRegionFromProfileArn(result.profileArn)
      const resolvedRegion = regionFromArn || result.region || config.value.region || 'us-east-1'
      config.value.region = resolvedRegion

      testedRefreshToken.value = nextRefreshToken
      testedCredential.value = {
        refreshToken: nextRefreshToken,
        accessToken: result.accessToken,
        expiresAt: result.expiresAt || '',
        profileArn: result.profileArn || '',
        region: resolvedRegion,
        authMethod: String(config.value.authMethod || 'social').toLowerCase() === 'idc' ? 'idc' : 'social',
        provider:
          String(config.value.authMethod || 'social').toLowerCase() === 'idc'
            ? 'BuilderId'
            : String(config.value.provider || '').trim(),
        clientId: config.value.clientId || '',
        clientSecret: config.value.clientSecret || ''
      }

      return result
    } finally {
      testingConfig.value = false
    }
  }

  async function fetchCurrentUsage(): Promise<KiroUsageResult> {
    const accessToken = String(config.value.accessToken || '').trim()
    if (!accessToken) {
      throw new Error(t('kiro.accessTokenRequiredForUsage'))
    }

    loadingUsage.value = true
    usageInfo.value = t('kiro.usageLoading')
    try {
      const result = await kiroApi.getUsage({
        accessToken,
        refreshToken: String(config.value.refreshToken || '').trim(),
        profileArn: String(config.value.profileArn || '').trim(),
        region: String(config.value.region || 'us-east-1').trim(),
        proxyUrl: String(config.value.proxyUrl || '').trim(),
        userAgent: String(config.value.userAgent || '').trim(),
        version: String(config.value.version || '').trim()
      })

      const subscriptionTitle = String(result?.subscriptionTitle || '').trim()
      const used = formatUsageNumber(result?.currentUsage)
      const limit = formatUsageNumber(result?.usageLimit)
      const balance = formatUsageNumber(result?.balance)
      const pct = formatUsagePct(result?.usagePct)

      let text = `${t('kiro.usageInfoPrefix')}${used} / ${limit}`
      if (pct) text += ` (${pct})`
      text += `, ${t('kiro.usageRemainingPrefix')}${balance}`

      if (subscriptionTitle) {
        text = `${t('kiro.usageSubscriptionPrefix')}${subscriptionTitle} · ${text}`
      }

      usageInfo.value = text
      return result
    } catch (error) {
      const httpDetail = parseKiroUsageHttpError(error)
      const detailMessage = httpDetail ? formatKiroUsageHttpError(httpDetail) : normalizeErrorMessage(error) || 'Unknown error'
      const msg = t('kiro.usageFailedPrefix') + detailMessage
      usageInfo.value = msg
      throw new Error(msg)
    } finally {
      loadingUsage.value = false
    }
  }

  async function ensureKiroEndpoint(): Promise<void> {
    try {
      const allEndpoints = await kiroApi.getAllEndpoints()
      const hasKiroEndpoint = allEndpoints.some((ep) => ep.transformer === 'kiro/claude' && ep.interfaceType === 'claude')
      if (hasKiroEndpoint) return

      const claudeEndpoints = allEndpoints.filter((ep) => ep.interfaceType === 'claude')
      const isOnlyClaudeEndpoint = claudeEndpoints.length === 0

      await kiroApi.saveEndpointData({
        id: 0,
        name: 'kiro',
        apiUrl: 'https://q.us-east-1.amazonaws.com',
        apiKey: '-',
        active: isOnlyClaudeEndpoint,
        enabled: true,
        interfaceType: 'claude',
        transformer: 'kiro/claude',
        transformerSet: true,
        priority: 8,
        remark: 'Auto-created Kiro endpoint'
      })
    } catch (error) {
      console.error('Failed to ensure kiro endpoint:', error)
    }
  }

  async function saveCurrentConfig(): Promise<void> {
    const refreshToken = String(config.value.refreshToken || '').trim()
    if (!refreshToken) {
      throw new Error(t('kiro.refreshTokenRequired'))
    }

    const isIdc = String(config.value.authMethod || 'social').toLowerCase() === 'idc'
    if (isIdc && (!String(config.value.clientId || '').trim() || !String(config.value.clientSecret || '').trim())) {
      throw new Error(t('kiro.idcFieldsRequired'))
    }

    if (refreshTokenChanged.value) {
      if (testedRefreshToken.value !== refreshToken || !testedCredential.value?.accessToken) {
        throw new Error(t('kiro.testRequired'))
      }
    }

    const shouldPersistAuthState = testedRefreshToken.value === refreshToken && !!testedCredential.value?.accessToken

    const payload: KiroConfig = {
      refreshToken: testedCredential.value?.refreshToken || refreshToken,
      region: testedCredential.value?.region || String(config.value.region || 'us-east-1').trim(),
      proxyUrl: String(config.value.proxyUrl || '').trim(),
      userAgent: String(config.value.userAgent || '').trim(),
      version: String(config.value.version || '').trim(),
      bufferedStream: !!config.value.bufferedStream,
      authMethod: String(config.value.authMethod || 'social').trim(),
      accessToken: shouldPersistAuthState ? testedCredential.value?.accessToken || '' : '',
      expiresAt: shouldPersistAuthState ? testedCredential.value?.expiresAt || '' : '',
      provider: '',
      profileArn: '',
      clientId: '',
      clientSecret: ''
    }

    if (isIdc) {
      payload.clientId = String(config.value.clientId || '').trim()
      payload.clientSecret = String(config.value.clientSecret || '').trim()
      payload.provider = testedCredential.value?.provider || loadedProvider.value || ''
      payload.profileArn = ''
    } else {
      payload.provider = testedCredential.value?.provider || String(config.value.provider || '').trim()
      payload.profileArn = shouldPersistAuthState ? testedCredential.value?.profileArn || '' : ''
      payload.clientId = ''
      payload.clientSecret = ''
    }

    savingConfig.value = true
    try {
      await kiroApi.saveConfig(payload)
      await ensureKiroEndpoint()
      initialRefreshToken.value = String(payload.refreshToken || '').trim()
      testedRefreshToken.value = ''
      testedCredential.value = null
    } finally {
      savingConfig.value = false
    }
  }

  async function loadGlobalConfig(): Promise<void> {
    loadingGlobalConfig.value = true
    try {
      const cfg = await kiroApi.getGlobalConfig()
      globalConfig.value = {
        ...defaultKiroGlobalConfig(),
        ...cfg,
        region: cfg?.region || 'us-east-1',
        rotationMode: cfg?.rotationMode || 'fixed',
        modelMapping: cfg?.modelMapping || {}
      }
    } finally {
      loadingGlobalConfig.value = false
    }
  }

  async function resetGlobalModelMappingDefaults(): Promise<Record<string, string>> {
    const defaults = await kiroApi.getDefaultModelMapping()
    globalConfig.value.modelMapping = defaults || {}
    return globalConfig.value.modelMapping
  }

  async function saveCurrentGlobalConfig(payload: KiroGlobalConfig): Promise<void> {
    savingGlobalConfig.value = true
    try {
      await kiroApi.saveGlobalConfig(payload)
      globalConfig.value = {
        ...defaultKiroGlobalConfig(),
        ...payload,
        modelMapping: payload.modelMapping || {}
      }
    } finally {
      savingGlobalConfig.value = false
    }
  }

  function clearUsageInfo(): void {
    usageInfo.value = ''
  }

  return {
    config,
    globalConfig,
    usageInfo,
    loadingConfig,
    testingConfig,
    savingConfig,
    loadingUsage,
    loadingGlobalConfig,
    savingGlobalConfig,
    refreshTokenChanged,
    canSaveConfig,
    canFetchUsage,
    loadConfig,
    markRefreshTokenInput,
    markIdcFieldInput,
    applyAuthCredential,
    testCurrentConfig,
    fetchCurrentUsage,
    saveCurrentConfig,
    clearUsageInfo,
    loadGlobalConfig,
    resetGlobalModelMappingDefaults,
    saveCurrentGlobalConfig
  }
})
