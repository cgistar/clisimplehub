import { onBeforeUnmount, reactive } from 'vue'
import { t } from '@/i18n/vue-i18n'
import { kiroApi } from '@/api/kiro'
import { generateCodeChallenge, generateRandomString, generateUUID } from '@/utils/helper'
import type { KiroAuthCredential } from '@/types/kiro'

type NotifyType = 'success' | 'error' | 'warning' | 'info'
type NotifyFn = (type: NotifyType, text: string) => void

type IdcFlow = 'standard' | 'org'
type IdcStatusKind = 'idle' | 'polling' | 'pending' | 'ok' | 'error'

type OnCredential = (credential: KiroAuthCredential) => Promise<void> | void

interface IdcRuntimeState {
  pollTimer: ReturnType<typeof setTimeout> | null
  pollIntervalMs: number
  pollInFlight: boolean
  pollingEnabled: boolean
  activeFlow: IdcFlow
  clientId: string
  clientSecret: string
  verifyUrl: string
  deviceCode: string
  region: string
  onCredential: OnCredential | null
}

interface SignRuntimeState {
  verifier: string
  state: string
  loginUrl: string
  isWaiting: boolean
  onCredential: OnCredential | null
}

interface SocialTokenResponse {
  refreshToken?: string
  accessToken?: string
  profileArn?: string
  expiresAt?: string
}

function toErrorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return String(error)
}

function getIdcProvider(flow: IdcFlow): string {
  return flow === 'org' ? 'Enterprise' : 'BuilderId'
}

function loginOptionToProvider(loginOption: string): string {
  const opt = String(loginOption || '').toLowerCase()
  if (opt === 'google') return 'Google'
  if (opt === 'github') return 'Github'
  return loginOption || 'Google'
}

function normalizeSocialTokenResponse(payload: unknown): SocialTokenResponse {
  if (!payload || typeof payload !== 'object') return {}
  const record = payload as Record<string, unknown>
  return {
    refreshToken: typeof record.refreshToken === 'string' ? record.refreshToken : undefined,
    accessToken: typeof record.accessToken === 'string' ? record.accessToken : undefined,
    profileArn: typeof record.profileArn === 'string' ? record.profileArn : undefined,
    expiresAt: typeof record.expiresAt === 'string' ? record.expiresAt : undefined
  }
}

export function useKiroAuthFlows(notify: NotifyFn) {
  const idcDeviceModal = reactive({
    show: false,
    verifyUrl: '',
    statusKind: 'idle' as IdcStatusKind,
    statusText: t('kiro.idcStatusIdle'),
    statusLabel: 'IDLE',
    loading: false
  })

  const idcOrgModal = reactive({
    show: false,
    step: 'config' as 'config' | 'verify',
    startUrl: '',
    region: 'us-east-1',
    verifyUrl: '',
    statusKind: 'idle' as IdcStatusKind,
    statusText: t('kiro.idcStatusIdle'),
    statusLabel: 'IDLE',
    loading: false
  })

  const kiroSignModal = reactive({
    show: false,
    waiting: false,
    loginUrl: ''
  })

  const idcState: IdcRuntimeState = {
    pollTimer: null,
    pollIntervalMs: 2000,
    pollInFlight: false,
    pollingEnabled: false,
    activeFlow: 'standard',
    clientId: '',
    clientSecret: '',
    verifyUrl: '',
    deviceCode: '',
    region: 'us-east-1',
    onCredential: null
  }

  const signState: SignRuntimeState = {
    verifier: '',
    state: '',
    loginUrl: '',
    isWaiting: false,
    onCredential: null
  }

  function setIdcStatus(flow: IdcFlow, kind: IdcStatusKind, text: string, label: string): void {
    const target = flow === 'org' ? idcOrgModal : idcDeviceModal
    target.statusKind = kind
    target.statusText = text
    target.statusLabel = label
  }

  function setVerifyUrl(flow: IdcFlow, url: string): void {
    idcState.verifyUrl = url
    if (flow === 'org') {
      idcOrgModal.verifyUrl = url
    } else {
      idcDeviceModal.verifyUrl = url
    }
  }

  function stopIdcPolling(): void {
    idcState.pollingEnabled = false
    if (idcState.pollTimer) {
      clearTimeout(idcState.pollTimer)
      idcState.pollTimer = null
    }
    idcState.pollInFlight = false
  }

  function scheduleNextIdcPoll(): void {
    if (!idcState.pollingEnabled) return
    if (idcState.pollTimer) clearTimeout(idcState.pollTimer)

    idcState.pollTimer = setTimeout(async () => {
      await pollIdcTokenOnce()
      scheduleNextIdcPoll()
    }, idcState.pollIntervalMs)
  }

  async function pollIdcTokenOnce(): Promise<void> {
    if (!idcState.pollingEnabled) return
    if (idcState.pollInFlight) return

    idcState.pollInFlight = true
    try {
      const result = await kiroApi.pollIdcToken({
        clientId: idcState.clientId,
        clientSecret: idcState.clientSecret,
        deviceCode: idcState.deviceCode,
        region: idcState.region || 'us-east-1'
      })

      if (result?.accessToken) {
        stopIdcPolling()
        setIdcStatus(idcState.activeFlow, 'ok', t('kiro.idcStatusSuccess'), 'SUCCESS')

        const credential: KiroAuthCredential = {
          refreshToken: result.refreshToken || '',
          accessToken: result.accessToken || '',
          region: idcState.region || 'us-east-1',
          authMethod: 'idc',
          provider: getIdcProvider(idcState.activeFlow),
          clientId: idcState.clientId,
          clientSecret: idcState.clientSecret
        }

        if (credential.refreshToken && idcState.onCredential) {
          await idcState.onCredential(credential)
        }

        setTimeout(() => {
          if (idcState.activeFlow === 'org') {
            closeIdcOrgLoginDialog()
          } else {
            closeIdcDeviceFlowDialog()
          }
          notify('success', t('kiro.idcLoginSuccess'))
        }, 900)
        return
      }

      if (result?.error === 'authorization_pending') {
        setIdcStatus(idcState.activeFlow, 'pending', t('kiro.idcStatusPending'), 'PENDING')
        return
      }

      if (result?.error === 'slow_down') {
        idcState.pollIntervalMs = Math.min(idcState.pollIntervalMs + 2000, 10000)
        setIdcStatus(
          idcState.activeFlow,
          'pending',
          `${t('kiro.idcStatusSlowDown')} ${(idcState.pollIntervalMs / 1000).toFixed(1)}s`,
          'PENDING'
        )
        return
      }

      if (result?.error === 'expired_token') {
        stopIdcPolling()
        setIdcStatus(idcState.activeFlow, 'error', t('kiro.idcStatusExpired'), 'EXPIRED')
        return
      }

      if (result?.error === 'access_denied') {
        stopIdcPolling()
        setIdcStatus(idcState.activeFlow, 'error', t('kiro.idcStatusAccessDenied'), 'ERROR')
        return
      }

      const nonRetryableErrors = ['invalid_client', 'invalid_grant', 'unauthorized_client', 'unsupported_grant_type']
      if (result?.error && nonRetryableErrors.includes(result.error)) {
        stopIdcPolling()
        setIdcStatus(idcState.activeFlow, 'error', `${t('kiro.idcStatusError')}: ${result.error}`, 'ERROR')
        return
      }

      const errorMsg = result?.error || 'Unknown error'
      setIdcStatus(idcState.activeFlow, 'pending', `${t('kiro.idcStatusPending')} (${errorMsg})`, 'PENDING')
    } catch (error) {
      const message = toErrorMessage(error)
      setIdcStatus(idcState.activeFlow, 'pending', `${t('kiro.idcStatusPending')} (${message})`, 'PENDING')
    } finally {
      idcState.pollInFlight = false
    }
  }

  async function startIdcFlow(params: {
    flow: IdcFlow
    region: string
    startUrl?: string
    onCredential: OnCredential
  }): Promise<void> {
    stopIdcPolling()

    idcState.activeFlow = params.flow
    idcState.onCredential = params.onCredential
    idcState.region = params.region || 'us-east-1'
    idcState.clientId = ''
    idcState.clientSecret = ''
    idcState.verifyUrl = ''
    idcState.deviceCode = ''
    idcState.pollIntervalMs = 2000

    setVerifyUrl(params.flow, '')
    setIdcStatus(params.flow, 'polling', t('kiro.idcStatusRegistering'), 'WORKING')

    if (params.flow === 'org') {
      idcOrgModal.loading = true
    } else {
      idcDeviceModal.loading = true
    }

    try {
      const registerResult = await kiroApi.registerIdcClient({ region: params.region })
      idcState.clientId = registerResult.clientId
      idcState.clientSecret = registerResult.clientSecret

      setIdcStatus(params.flow, 'polling', t('kiro.idcStatusGettingAuth'), 'WORKING')

      const deviceAuthResult = await kiroApi.startDeviceAuthorization({
        clientId: idcState.clientId,
        clientSecret: idcState.clientSecret,
        region: params.region,
        startUrl: params.startUrl || ''
      })

      idcState.verifyUrl = deviceAuthResult.verificationUriComplete || deviceAuthResult.verificationUri || ''
      idcState.deviceCode = deviceAuthResult.deviceCode || ''
      idcState.pollIntervalMs = Math.max(2000, (deviceAuthResult.interval || 2) * 1000)

      setVerifyUrl(params.flow, idcState.verifyUrl)
      setIdcStatus(params.flow, 'pending', t('kiro.idcStatusPending'), 'PENDING')

      idcState.pollingEnabled = true
      pollIdcTokenOnce().finally(() => {
        scheduleNextIdcPoll()
      })
    } catch (error) {
      stopIdcPolling()
      setIdcStatus(params.flow, 'error', `${t('kiro.idcStatusError')}: ${toErrorMessage(error)}`, 'ERROR')
      throw error
    } finally {
      if (params.flow === 'org') {
        idcOrgModal.loading = false
      } else {
        idcDeviceModal.loading = false
      }
    }
  }

  async function openUrl(url: string, incognito = false): Promise<void> {
    if (!url) return

    if (incognito) {
      await kiroApi.openURLInIncognito(url)
      return
    }

    if (window.runtime?.BrowserOpenURL) {
      window.runtime.BrowserOpenURL(url)
    } else {
      window.open(url, '_blank', 'noopener,noreferrer')
    }
  }

  async function openUrlWithFallback(url: string): Promise<void> {
    if (!url) return
    try {
      await openUrl(url, true)
    } catch {
      if (window.runtime?.BrowserOpenURL) {
        window.runtime.BrowserOpenURL(url)
      } else {
        window.open(url, '_blank', 'noopener,noreferrer')
      }
    }
  }

  async function startIdcDeviceFlowLogin(region: string, onCredential: OnCredential): Promise<void> {
    closeIdcOrgLoginDialog()
    idcDeviceModal.show = true
    idcDeviceModal.verifyUrl = ''

    try {
      await startIdcFlow({
        flow: 'standard',
        region: region || 'us-east-1',
        onCredential
      })
    } catch (error) {
      notify('error', toErrorMessage(error))
    }
  }

  function closeIdcDeviceFlowDialog(): void {
    stopIdcPolling()
    idcDeviceModal.show = false
    idcDeviceModal.verifyUrl = ''
  }

  function startIdcOrgLogin(defaultRegion: string, onCredential: OnCredential): void {
    closeIdcDeviceFlowDialog()
    idcState.onCredential = onCredential
    idcOrgModal.show = true
    idcOrgModal.step = 'config'
    idcOrgModal.startUrl = ''
    idcOrgModal.region = defaultRegion || 'us-east-1'
    idcOrgModal.verifyUrl = ''
    setIdcStatus('org', 'idle', t('kiro.idcStatusIdle'), 'IDLE')
  }

  async function submitIdcOrgLogin(): Promise<void> {
    const startUrl = String(idcOrgModal.startUrl || '').trim()
    const region = String(idcOrgModal.region || '').trim() || 'us-east-1'

    if (!startUrl) {
      throw new Error(t('kiro.idcOrgStartUrlRequired'))
    }

    idcOrgModal.step = 'verify'

    try {
      await startIdcFlow({
        flow: 'org',
        region,
        startUrl,
        onCredential: idcState.onCredential || (async () => {})
      })
    } catch (error) {
      idcOrgModal.step = 'config'
      throw error
    }
  }

  function backToOrgLoginStep1(): void {
    stopIdcPolling()
    idcOrgModal.step = 'config'
  }

  function closeIdcOrgLoginDialog(): void {
    stopIdcPolling()
    idcOrgModal.show = false
    idcOrgModal.step = 'config'
    idcOrgModal.verifyUrl = ''
  }

  async function copyIdcVerifyUrl(): Promise<void> {
    const target = idcState.activeFlow === 'org' ? idcOrgModal.verifyUrl : idcDeviceModal.verifyUrl
    if (!target) return

    await navigator.clipboard.writeText(target)
    setIdcStatus(idcState.activeFlow, 'pending', t('kiro.idcLinkCopied'), 'PENDING')
  }

  async function openIdcVerifyUrl(): Promise<void> {
    const target = idcState.activeFlow === 'org' ? idcOrgModal.verifyUrl : idcDeviceModal.verifyUrl
    if (!target) return
    await openUrl(target)
    setIdcStatus(idcState.activeFlow, 'pending', t('kiro.idcLinkOpened'), 'PENDING')
  }

  function resetSignState(): void {
    signState.verifier = ''
    signState.state = ''
    signState.loginUrl = ''
    signState.isWaiting = false
    signState.onCredential = null
    kiroSignModal.loginUrl = ''
    kiroSignModal.waiting = false
    if (window.runtime?.EventsOff) {
      window.runtime.EventsOff('kiro-sign-callback')
      window.runtime.EventsOff('kiro-sign-timeout')
      window.runtime.EventsOff('kiro-sign-idc-callback')
      window.runtime.EventsOff('kiro-sign-idc-timeout')
    }
  }

  async function exchangeSocialCodeForToken(code: string, verifier: string, redirectUri: string): Promise<SocialTokenResponse> {
    const tryRequests: Array<{ url: string; body: Record<string, unknown> }> = [
      {
        url: 'https://app.kiro.dev/oauth/token',
        body: {
          grant_type: 'authorization_code',
          code,
          code_verifier: verifier,
          redirect_uri: redirectUri
        }
      },
      {
        url: 'https://app.kiro.dev/api/oauth/token',
        body: {
          code,
          codeVerifier: verifier,
          redirectUri
        }
      }
    ]

    for (const candidate of tryRequests) {
      try {
        const response = await fetch(candidate.url, {
          method: 'POST',
          headers: {
            'content-type': 'application/json'
          },
          body: JSON.stringify(candidate.body)
        })

        if (!response.ok) continue
        const json = await response.json()
        const normalized = normalizeSocialTokenResponse(json)
        if (normalized.refreshToken || normalized.accessToken) {
          return normalized
        }
      } catch {
        // Try next endpoint/format.
      }
    }

    throw new Error(t('kiro.kiroSignSocialExchangeFailed'))
  }

  async function handleKiroSignIdcAuthCodeFlow(data: Record<string, string>): Promise<void> {
    const region = data.idcRegion || 'us-east-1'
    const issuerUrl = data.issuerUrl || 'https://view.awsapps.com/start'
    const provider = String(data.loginOption || '').toLowerCase() === 'builderid' ? 'BuilderId' : 'Enterprise'

    const regResp = await kiroApi.registerIdcAuthCodeClient({
      region,
      issuerUrl
    })

    const verifier = generateRandomString(128)
    const challenge = await generateCodeChallenge(verifier)
    const state = generateUUID()
    const port = await kiroApi.startKiroSignIdcCallbackServer()

    const redirectUri = `http://127.0.0.1:${port}/oauth/callback`
    const scopes = [
      'codewhisperer:completions',
      'codewhisperer:analysis',
      'codewhisperer:conversations',
      'codewhisperer:transformations',
      'codewhisperer:taskassist'
    ].join(',')

    const authorizeUrl =
      `https://oidc.${region}.amazonaws.com/authorize?` +
      `response_type=code&` +
      `client_id=${encodeURIComponent(regResp.clientId)}&` +
      `redirect_uri=${encodeURIComponent(redirectUri)}&` +
      `scopes=${encodeURIComponent(scopes)}&` +
      `state=${state}&` +
      `code_challenge=${challenge}&` +
      `code_challenge_method=S256`

    const codeResult = await new Promise<Record<string, string>>((resolve, reject) => {
      if (window.runtime?.EventsOn) {
        window.runtime.EventsOn('kiro-sign-idc-callback', (payload: Record<string, string>) => {
          if (window.runtime?.EventsOff) {
            window.runtime.EventsOff('kiro-sign-idc-callback')
            window.runtime.EventsOff('kiro-sign-idc-timeout')
          }
          if (payload?.state !== state) {
            reject(new Error('IDC auth code state mismatch'))
            return
          }
          resolve(payload)
        })

        window.runtime.EventsOn('kiro-sign-idc-timeout', () => {
          if (window.runtime?.EventsOff) {
            window.runtime.EventsOff('kiro-sign-idc-callback')
            window.runtime.EventsOff('kiro-sign-idc-timeout')
          }
          reject(new Error(t('kiro.kiroSignLoginTimeout')))
        })
      } else {
        reject(new Error('Runtime event bridge is unavailable'))
      }

      openUrlWithFallback(authorizeUrl).catch(reject)
    })

    const tokenResp = await kiroApi.exchangeIdcAuthCode({
      region,
      clientId: regResp.clientId,
      clientSecret: regResp.clientSecret,
      code: codeResult.code,
      redirectUri,
      codeVerifier: verifier
    })

    if (!tokenResp.refreshToken && !tokenResp.accessToken) {
      throw new Error(t('kiro.kiroSignLoginFailed'))
    }

    const credential: KiroAuthCredential = {
      refreshToken: tokenResp.refreshToken || '',
      accessToken: tokenResp.accessToken || '',
      region,
      authMethod: 'idc',
      provider,
      clientId: regResp.clientId,
      clientSecret: regResp.clientSecret
    }

    await signState.onCredential?.(credential)
  }

  async function handleKiroSignCallback(payload: Record<string, string>): Promise<void> {
    if (!signState.isWaiting) return
    if ((payload?.state || '') !== signState.state) {
      throw new Error('State mismatch')
    }

    if ((payload?.type || '').toLowerCase() === 'idc') {
      await handleKiroSignIdcAuthCodeFlow(payload)
      return
    }

    const loginOption = payload?.loginOption || 'google'
    const provider = loginOptionToProvider(loginOption)

    let tokenResponse = normalizeSocialTokenResponse(payload)
    if (!tokenResponse.refreshToken && !tokenResponse.accessToken) {
      tokenResponse = await exchangeSocialCodeForToken(
        payload.code || '',
        signState.verifier,
        `http://localhost:3128/oauth/callback?login_option=${encodeURIComponent(loginOption)}`
      )
    }

    const credential: KiroAuthCredential = {
      refreshToken: tokenResponse.refreshToken || '',
      accessToken: tokenResponse.accessToken || '',
      profileArn: tokenResponse.profileArn || '',
      expiresAt: tokenResponse.expiresAt || '',
      region: 'us-east-1',
      authMethod: 'social',
      provider
    }

    if (!credential.refreshToken && !credential.accessToken) {
      throw new Error(t('kiro.kiroSignSocialExchangeFailed'))
    }

    await signState.onCredential?.(credential)
  }

  function handleKiroSignTimeout(): void {
    if (!signState.isWaiting) return
    notify('error', t('kiro.kiroSignLoginTimeout'))
    void closeKiroSignLoginModal()
  }

  async function startKiroSignLogin(onCredential: OnCredential): Promise<void> {
    const verifier = generateRandomString(128)
    const challenge = await generateCodeChallenge(verifier)
    const state = generateUUID()
    const redirectUri = 'http://localhost:3128'

    const loginUrl =
      `https://app.kiro.dev/signin?` +
      `state=${state}&` +
      `code_challenge=${challenge}&` +
      `code_challenge_method=S256&` +
      `redirect_uri=${encodeURIComponent(redirectUri)}&` +
      `redirect_from=KiroIDE`

    signState.verifier = verifier
    signState.state = state
    signState.loginUrl = loginUrl
    signState.isWaiting = true
    signState.onCredential = onCredential

    kiroSignModal.show = true
    kiroSignModal.waiting = true
    kiroSignModal.loginUrl = loginUrl

    try {
      await kiroApi.startKiroSignCallbackServer()
    } catch (error) {
      const message = toErrorMessage(error)
      if (message.includes('3128')) {
        throw new Error(t('kiro.kiroSignLoginPortInUse'))
      }
      throw error
    }

    if (window.runtime?.EventsOn) {
      window.runtime.EventsOn('kiro-sign-callback', async (payload: Record<string, string>) => {
        try {
          await handleKiroSignCallback(payload)
          notify('success', t('kiro.kiroSignLoginSuccess'))
          await closeKiroSignLoginModal()
        } catch (error) {
          notify('error', t('kiro.kiroSignLoginFailed') + ': ' + toErrorMessage(error))
          await closeKiroSignLoginModal()
        }
      })
      window.runtime.EventsOn('kiro-sign-timeout', handleKiroSignTimeout)
    }

  }

  async function closeKiroSignLoginModal(): Promise<void> {
    kiroSignModal.show = false
    kiroSignModal.waiting = false
    try {
      await kiroApi.stopKiroSignCallbackServer()
    } catch {
      // Ignore close errors.
    }
    try {
      await kiroApi.stopKiroSignIdcCallbackServer()
    } catch {
      // Ignore close errors.
    }
    resetSignState()
  }

  async function copyKiroSignLoginUrl(): Promise<void> {
    if (!signState.loginUrl) return
    await navigator.clipboard.writeText(signState.loginUrl)
    notify('success', t('kiro.linkCopied'))
  }

  async function openKiroSignLoginUrl(incognito = false): Promise<void> {
    await openUrl(signState.loginUrl, incognito)
  }

  onBeforeUnmount(() => {
    stopIdcPolling()
    void closeKiroSignLoginModal()
  })

  return {
    idcDeviceModal,
    idcOrgModal,
    kiroSignModal,
    startIdcDeviceFlowLogin,
    closeIdcDeviceFlowDialog,
    startIdcOrgLogin,
    submitIdcOrgLogin,
    backToOrgLoginStep1,
    closeIdcOrgLoginDialog,
    copyIdcVerifyUrl,
    openIdcVerifyUrl,
    startKiroSignLogin,
    closeKiroSignLoginModal,
    copyKiroSignLoginUrl,
    openKiroSignLoginUrl
  }
}
