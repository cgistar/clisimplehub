import { useEffect, useMemo, useState, type FormEvent } from 'react'
import { toast } from 'sonner'
import { ROUTES, routeFromPath } from '@/constants/routes'
import { webApi } from '@/api/web'
import type { ApiError } from '@/api/client'
import { copyToClipboard } from '@/lib/format'
import { buildCodexAccountCopyData, buildCodexAccountsCopyJson, createCodexConfigForm, createCodexEditForm } from '@/lib/codex'
import { buildXaiAccountCopyJson, buildXaiAccountsCopyJson, createXaiConfigForm, createXaiEditForm } from '@/lib/xai'
import type { ActionResponse, ClashPageData, CodexAccount, CodexConfigForm, CodexEditForm, CodexPageData, EndpointInfo, HomePageData, RouteKey, SettingsData, SettingsForm, XaiAccount, XaiConfigForm, XaiEditForm, XaiPageData } from '@/types'
import LoginScreen from '@/components/LoginScreen'
import Topbar from '@/components/Topbar'
import PageHeader from '@/components/PageHeader'
import HomePage from '@/pages/HomePage'
import CodexPage from '@/pages/CodexPage'
import XaiPage from '@/pages/XaiPage'
import ProxyPage from '@/pages/ProxyPage'
import SettingsPage from '@/pages/SettingsPage'
import CodexConfigDialog from '@/pages/components/CodexConfigDialog'
import CodexEditDialog from '@/pages/components/CodexEditDialog'
import CodexImportDialog from '@/pages/components/CodexImportDialog'
import XaiConfigDialog from '@/pages/components/XaiConfigDialog'
import XaiEditDialog from '@/pages/components/XaiEditDialog'
import XaiImportDialog from '@/pages/components/XaiImportDialog'
import XaiSSOImportDialog from '@/pages/components/XaiSSOImportDialog'
import EndpointImportDialog from '@/pages/components/EndpointImportDialog'
import HomeStatsDialog from '@/pages/components/HomeStatsDialog'
import { Toaster } from '@/components/ui/sonner'

export default function App() {
  const [route, setRoute] = useState<RouteKey>(routeFromPath(window.location.pathname))
  const [sessionLoading, setSessionLoading] = useState<boolean>(true)
  const [authenticated, setAuthenticated] = useState<boolean>(false)
  const [hasApiKey, setHasApiKey] = useState<boolean>(true)
  const [proxyAvailable, setProxyAvailable] = useState<boolean>(false)
  const [loginKey, setLoginKey] = useState<string>('')
  const [loginLoading, setLoginLoading] = useState<boolean>(false)
  const [pageLoading, setPageLoading] = useState<boolean>(false)
  const [globalError, setGlobalError] = useState<string>('')
  const [busyAction, setBusyAction] = useState<string>('')
  const [homeData, setHomeData] = useState<HomePageData | null>(null)
  const [codexData, setCodexData] = useState<CodexPageData | null>(null)
  const [xaiData, setXaiData] = useState<XaiPageData | null>(null)
  const [clashData, setClashData] = useState<ClashPageData | null>(null)
  const [settingsData, setSettingsData] = useState<SettingsData | null>(null)
  const [settingsForm, setSettingsForm] = useState<SettingsForm | null>(null)
  const [settingsSaving, setSettingsSaving] = useState<boolean>(false)
  const [codexConfigDialogOpen, setCodexConfigDialogOpen] = useState<boolean>(false)
  const [codexConfigForm, setCodexConfigForm] = useState<CodexConfigForm>(createCodexConfigForm())
  const [codexConfigSaving, setCodexConfigSaving] = useState<boolean>(false)
  const [codexEditDialogOpen, setCodexEditDialogOpen] = useState<boolean>(false)
  const [codexEditForm, setCodexEditForm] = useState<CodexEditForm>(createCodexEditForm())
  const [codexEditSaving, setCodexEditSaving] = useState<boolean>(false)
  const [codexImportDialogOpen, setCodexImportDialogOpen] = useState<boolean>(false)
  const [xaiConfigDialogOpen, setXaiConfigDialogOpen] = useState<boolean>(false)
  const [xaiConfigForm, setXaiConfigForm] = useState<XaiConfigForm>(createXaiConfigForm())
  const [xaiConfigSaving, setXaiConfigSaving] = useState<boolean>(false)
  const [xaiEditDialogOpen, setXaiEditDialogOpen] = useState<boolean>(false)
  const [xaiEditForm, setXaiEditForm] = useState<XaiEditForm>(createXaiEditForm())
  const [xaiEditSaving, setXaiEditSaving] = useState<boolean>(false)
  const [xaiImportDialogOpen, setXaiImportDialogOpen] = useState<boolean>(false)
  const [xaiSSOImportDialogOpen, setXaiSSOImportDialogOpen] = useState<boolean>(false)
  const [homeStatsDialogOpen, setHomeStatsDialogOpen] = useState<boolean>(false)
  const [endpointImportDialogOpen, setEndpointImportDialogOpen] = useState<boolean>(false)

  useEffect(() => {
    const onPopState = () => setRoute(routeFromPath(window.location.pathname))
    window.addEventListener('popstate', onPopState)
    return () => window.removeEventListener('popstate', onPopState)
  }, [])

  useEffect(() => {
    void (async () => {
      try {
        const data = await webApi.getSession()
        setAuthenticated(Boolean(data.authenticated))
        setHasApiKey(Boolean(data.hasApiKey))
        setProxyAvailable(Boolean(data.proxyAvailable))
      } catch (error) {
        const message = (error as ApiError).message || '会话状态获取失败'
        setGlobalError(message)
        toast.error(message)
      } finally {
        setSessionLoading(false)
      }
    })()
  }, [])

  useEffect(() => {
    if (!authenticated) return
    void refreshCurrentPage(route)
  }, [authenticated, route])

  useEffect(() => {
    if (!authenticated || (route !== 'codex' && route !== 'xai')) return
    const timer = setInterval(() => void refreshCurrentPage(route), 60_000)
    return () => clearInterval(timer)
  }, [authenticated, route])

  useEffect(() => {
    if (!settingsData) return
    setSettingsForm({
      port: settingsData.port || 5600,
      apiKey: settingsData.apiKey || '',
      fallback: Boolean(settingsData.fallback),
      debugMode: settingsData.debugMode || '',
      listenAddr: settingsData.listenAddr || '0.0.0.0',
      proxyUrl: settingsData.proxyUrl || '',
      clashPath: settingsData.clashPath || '',
      dbSource: settingsData.dbSource || '',
      disableImageGeneration: settingsData.disableImageGeneration || 'off',
    })
  }, [settingsData])

  async function refreshCurrentPage(currentRoute: RouteKey): Promise<void> {
    setPageLoading(true)
    try {
      if (currentRoute === 'codex') {
        const data = await webApi.getCodex()
        setCodexData(data)
        if (!codexConfigDialogOpen) {
          setCodexConfigForm(createCodexConfigForm(data?.globalConfig))
        }
        return
      }
      if (currentRoute === 'xai') {
        const data = await webApi.getXai()
        setXaiData(data)
        if (!xaiConfigDialogOpen) {
          setXaiConfigForm(createXaiConfigForm(data?.globalConfig))
        }
        return
      }
      if (currentRoute === 'proxy') {
        const data = await webApi.getClash()
        setClashData(data)
        setProxyAvailable(Boolean(data.available))
        if (!data.available) navigate('home')
        return
      }
      if (currentRoute === 'settings') {
        setSettingsData(await webApi.getSettings())
        return
      }
      setHomeData(await webApi.getHome())
    } catch (error) {
      const apiError = error as ApiError
      if (apiError.status === 401 || apiError.status === 403) {
        setAuthenticated(false)
        setGlobalError('登录状态已失效，请重新登录')
        return
      }
      toast.error(apiError.message || '页面数据加载失败')
    } finally {
      setPageLoading(false)
    }
  }

  function navigate(nextRoute: RouteKey): void {
    const target = ROUTES[nextRoute]
    if (window.location.pathname !== target) {
      history.pushState({}, '', target)
    }
    setRoute(nextRoute)
    setGlobalError('')
  }

  async function handleLogin(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault()
    setLoginLoading(true)
    setGlobalError('')
    try {
      await webApi.login(loginKey)
      setAuthenticated(true)
      setLoginKey('')
      setGlobalError('')
      toast.success('登录成功')
    } catch (error) {
      setGlobalError((error as ApiError).message || '登录失败')
    } finally {
      setLoginLoading(false)
    }
  }

  async function handleLogout(): Promise<void> {
    try {
      await webApi.logout()
    } catch {
      // 忽略登出异常
    }
    setAuthenticated(false)
    setGlobalError('')
    setHomeData(null)
    setCodexData(null)
    setXaiData(null)
    setClashData(null)
    setProxyAvailable(false)
    setSettingsData(null)
    setSettingsForm(null)
    setCodexConfigDialogOpen(false)
    setCodexImportDialogOpen(false)
    setXaiConfigDialogOpen(false)
    setXaiImportDialogOpen(false)
    setXaiSSOImportDialogOpen(false)
    setHomeStatsDialogOpen(false)
    setEndpointImportDialogOpen(false)
  }

  async function handleActivateEndpoint(interfaceType: string, endpointId: number): Promise<void> {
    const actionKey = `endpoint:${endpointId}`
    setBusyAction(actionKey)
    try {
      const result = await webApi.setActiveEndpoint(interfaceType, endpointId)
      toast.success(result.message || '已切换活跃端点')
      await refreshCurrentPage('home')
    } catch (error) {
      const apiError = error as ApiError
      if (apiError.status === 401 || apiError.status === 403) {
        setAuthenticated(false)
        setGlobalError('登录状态已失效，请重新登录')
      } else {
        toast.error(apiError.message || '切换活跃端点失败')
      }
    } finally {
      setBusyAction('')
    }
  }

  async function handleDeleteEndpoint(endpoint: EndpointInfo): Promise<void> {
    const displayName = endpoint.providerName ? `${endpoint.providerName} - ${endpoint.name}` : endpoint.name
    if (!window.confirm(`确定删除端点 ${displayName} 吗？`)) return

    const actionKey = `endpoint:delete:${endpoint.id}`
    setBusyAction(actionKey)
    try {
      const result = await webApi.deleteEndpoint(endpoint.id)
      toast.success(result.message || '端点已删除')
      await refreshCurrentPage('home')
    } catch (error) {
      const apiError = error as ApiError
      if (apiError.status === 401 || apiError.status === 403) {
        setAuthenticated(false)
        setGlobalError('登录状态已失效，请重新登录')
      } else {
        toast.error(apiError.message || '删除端点失败')
      }
    } finally {
      setBusyAction('')
    }
  }

  async function runCodexAction(actionKey: string, request: () => Promise<ActionResponse>, defaultSuccessMessage: string, defaultErrorMessage: string): Promise<ActionResponse | null> {
    setBusyAction(actionKey)
    try {
      const result = await request()
      toast.success(result?.message || defaultSuccessMessage)
      await refreshCurrentPage('codex')
      return result
    } catch (error) {
      const apiError = error as ApiError
      if (apiError.status === 401 || apiError.status === 403) {
        setAuthenticated(false)
        setGlobalError('登录状态已失效，请重新登录')
      } else {
        toast.error(apiError.message || defaultErrorMessage)
      }
      return null
    } finally {
      setBusyAction('')
    }
  }

  async function handleActivateAccount(accountId: string): Promise<void> {
    await runCodexAction(`codex:activate:${accountId}`, () => webApi.activateCodexAccount(accountId), '已切换活跃账号', '切换 Codex 活跃账号失败')
  }

  async function handleRefreshCodexToken(accountId: string): Promise<void> {
    await runCodexAction(`codex:refresh:${accountId}`, () => webApi.refreshCodexToken(accountId), 'Refresh Token 成功', 'Refresh Token 失败')
  }

  async function handleFetchCodexUsage(accountId: string): Promise<void> {
    await runCodexAction(`codex:usage:${accountId}`, () => webApi.fetchCodexUsage(accountId), '账号用量已更新', '获取 Codex 用量失败')
  }

  async function handleFetchCodexPrimaryUsage(accountId: string): Promise<void> {
    await runCodexAction(`codex:usage-primary:${accountId}`, () => webApi.fetchCodexPrimaryUsage(accountId), '5 小时用量已更新', '获取 Codex 5 小时用量失败')
  }

  async function handleConsumeCodexResetCredit(accountId: string, creditId: string): Promise<void> {
    const result = await runCodexAction(
      `codex:reset:${accountId}`,
      () => webApi.consumeCodexResetCredit(accountId, creditId),
      '周限已重置',
      '重置 Codex 周限失败',
    )
    if (!result) {
      throw new Error('重置 Codex 周限失败')
    }
  }

  async function handleDeleteCodexAccount(accountId: string): Promise<void> {
    await runCodexAction(`codex:delete:${accountId}`, () => webApi.deleteCodexAccount(accountId), '账号已删除', '删除 Codex 账号失败')
  }

  async function runXaiAction(actionKey: string, request: () => Promise<ActionResponse>, defaultSuccessMessage: string, defaultErrorMessage: string): Promise<ActionResponse | null> {
    setBusyAction(actionKey)
    try {
      const result = await request()
      toast.success(result?.message || defaultSuccessMessage)
      await refreshCurrentPage('xai')
      return result
    } catch (error) {
      const apiError = error as ApiError
      if (apiError.status === 401 || apiError.status === 403) {
        setAuthenticated(false)
        setGlobalError('登录状态已失效，请重新登录')
      } else {
        toast.error(apiError.message || defaultErrorMessage)
      }
      return null
    } finally {
      setBusyAction('')
    }
  }

  async function handleActivateXaiAccount(accountId: string): Promise<void> {
    await runXaiAction(`xai:activate:${accountId}`, () => webApi.activateXaiAccount(accountId), '已切换活跃账号', '切换 xAI 活跃账号失败')
  }

  async function handleProbeXaiStream(accountId: string): Promise<void> {
    await runXaiAction(`xai:probe:${accountId}`, () => webApi.probeXaiStream(accountId), '连通测试成功', '连通测试失败')
  }

  async function handleRefreshXaiQuota(accountId: string): Promise<void> {
    await runXaiAction(`xai:quota:${accountId}`, () => webApi.refreshXaiQuota(accountId), '额度已刷新', '刷新额度失败')
  }

  async function handleRefreshXaiToken(accountId: string): Promise<void> {
    await runXaiAction(`xai:refresh:${accountId}`, () => webApi.refreshXaiToken(accountId), 'Refresh Token 成功', 'Refresh Token 失败')
  }

  async function handleXaiSSO2Auth(accountId: string): Promise<void> {
    setBusyAction(`xai:sso2auth:${accountId}`)
    try {
      const result = await webApi.sso2authXaiAccount(accountId)
      const warning = String(result?.warning || '').trim()
      if (warning) toast.warning(`OAuth 凭据已更新，但身份信息补全失败：${warning}`)
      else toast.success(result?.message || 'SSO2Auth 完成，OAuth 凭据已更新')
      await refreshCurrentPage('xai')
    } catch (error) {
      const apiError = error as ApiError
      if (apiError.status === 401 || apiError.status === 403) {
        setAuthenticated(false)
        setGlobalError('登录状态已失效，请重新登录')
      } else {
        toast.error(apiError.message || 'SSO2Auth 失败')
      }
    } finally {
      setBusyAction('')
    }
  }

  async function handleSetXaiAutoRefreshToken(enabled: boolean): Promise<void> {
    await runXaiAction(
      'xai:auto-refresh',
      () => webApi.setXaiAutoRefreshToken(enabled),
      enabled ? 'Token 自动更新已开启' : 'Token 自动更新已关闭',
      '保存 Token 自动更新配置失败',
    )
  }

  async function handleDeleteXaiAccount(accountId: string): Promise<void> {
    await runXaiAction(`xai:delete:${accountId}`, () => webApi.deleteXaiAccount(accountId), '账号已删除', '删除 xAI 账号失败')
  }

  async function handleCopyXaiAccount(account: XaiAccount): Promise<void> {
    try {
      await copyToClipboard(buildXaiAccountCopyJson(account))
      toast.success('auth.json 已复制到剪贴板')
    } catch (error) {
      toast.error((error as ApiError).message || '复制账号信息失败')
    }
  }

  async function handleCopyVisibleXaiAccounts(accounts: XaiAccount[]): Promise<void> {
    try {
      await copyToClipboard(buildXaiAccountsCopyJson(accounts))
      toast.success('当前显示账号已复制到剪贴板')
    } catch (error) {
      toast.error((error as ApiError).message || '复制当前显示账号失败')
    }
  }

  function handleOpenXaiConfig(): void {
    setXaiConfigForm(createXaiConfigForm(xaiData?.globalConfig))
    setXaiConfigDialogOpen(true)
  }

  function handleCloseXaiConfig(): void {
    if (xaiConfigSaving) return
    setXaiConfigDialogOpen(false)
  }

  function handleOpenXaiEdit(account: XaiAccount): void {
    setXaiEditForm(createXaiEditForm(account))
    setXaiEditDialogOpen(true)
  }

  function handleCloseXaiEdit(): void {
    if (xaiEditSaving) return
    setXaiEditDialogOpen(false)
  }

  async function handleSaveXaiConfig(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault()
    setXaiConfigSaving(true)
    try {
      const result = await webApi.saveXaiConfig(xaiConfigForm)
      toast.success(result.message || 'xAI 配置已保存')
      setXaiConfigDialogOpen(false)
      await refreshCurrentPage('xai')
    } catch (error) {
      toast.error((error as ApiError).message || '保存 xAI 配置失败')
    } finally {
      setXaiConfigSaving(false)
    }
  }

  async function handleSaveXaiEdit(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault()
    setXaiEditSaving(true)
    try {
      const result = await webApi.updateXaiAccount(xaiEditForm)
      toast.success(result.message || '账号已更新')
      setXaiEditDialogOpen(false)
      await refreshCurrentPage('xai')
    } catch (error) {
      toast.error((error as ApiError).message || '更新 xAI 账号失败')
    } finally {
      setXaiEditSaving(false)
    }
  }

  async function handleCopyCodexAccount(account: CodexAccount): Promise<void> {
    try {
      await copyToClipboard(JSON.stringify(buildCodexAccountCopyData(account), null, 2))
      toast.success('账号信息已复制到剪贴板')
    } catch (error) {
      toast.error((error as ApiError).message || '复制账号信息失败')
    }
  }

  async function handleCopyVisibleCodexAccounts(accounts: CodexAccount[]): Promise<void> {
    try {
      await copyToClipboard(buildCodexAccountsCopyJson(accounts))
      toast.success('当前显示账号已复制到剪贴板')
    } catch (error) {
      toast.error((error as ApiError).message || '复制当前显示账号失败')
    }
  }

  function handleOpenCodexConfig(): void {
    setCodexConfigForm(createCodexConfigForm(codexData?.globalConfig))
    setCodexConfigDialogOpen(true)
  }

  function handleCloseCodexConfig(): void {
    if (codexConfigSaving) return
    setCodexConfigDialogOpen(false)
  }

  function handleOpenCodexEdit(account: CodexAccount): void {
    setCodexEditForm(createCodexEditForm(account))
    setCodexEditDialogOpen(true)
  }

  function handleCloseCodexEdit(): void {
    if (codexEditSaving) return
    setCodexEditDialogOpen(false)
  }

  function handleOpenCodexImport(): void {
    setCodexImportDialogOpen(true)
  }

  function handleCloseCodexImport(): void {
    setCodexImportDialogOpen(false)
  }

  function handleAuthExpired(): void {
    setAuthenticated(false)
    setGlobalError('登录状态已失效，请重新登录')
    setHomeStatsDialogOpen(false)
    setEndpointImportDialogOpen(false)
    setXaiSSOImportDialogOpen(false)
  }

  async function handleSaveCodexConfig(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault()
    setCodexConfigSaving(true)
    try {
      const result = await webApi.saveCodexConfig(codexConfigForm)
      toast.success(result.message || 'Codex 配置已保存')
      setCodexConfigDialogOpen(false)
      await refreshCurrentPage('codex')
    } catch (error) {
      const apiError = error as ApiError
      if (apiError.status === 401 || apiError.status === 403) {
        setAuthenticated(false)
        setGlobalError('登录状态已失效，请重新登录')
      } else {
        toast.error(apiError.message || '保存 Codex 配置失败')
      }
    } finally {
      setCodexConfigSaving(false)
    }
  }

  async function handleSaveCodexEdit(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault()
    setCodexEditSaving(true)
    try {
      const result = await webApi.updateCodexAccount(codexEditForm)
      toast.success(result.message || '账号已更新')
      setCodexEditDialogOpen(false)
      await refreshCurrentPage('codex')
    } catch (error) {
      const apiError = error as ApiError
      if (apiError.status === 401 || apiError.status === 403) {
        setAuthenticated(false)
        setGlobalError('登录状态已失效，请重新登录')
      } else {
        toast.error(apiError.message || '更新 Codex 账号失败')
      }
    } finally {
      setCodexEditSaving(false)
    }
  }

  async function handleRestoreCodexAccount(accountId: string): Promise<void> {
    setCodexEditSaving(true)
    try {
      const result = await webApi.restoreCodexAccount(accountId)
      toast.success(result.message || '账号已恢复正常')
      setCodexEditDialogOpen(false)
      await refreshCurrentPage('codex')
    } catch (error) {
      const apiError = error as ApiError
      if (apiError.status === 401 || apiError.status === 403) {
        setAuthenticated(false)
        setGlobalError('登录状态已失效，请重新登录')
      } else {
        toast.error(apiError.message || '恢复 Codex 账号失败')
      }
    } finally {
      setCodexEditSaving(false)
    }
  }

  async function handleSaveSettings(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault()
    if (!settingsForm) return

    setSettingsSaving(true)
    try {
      const result = await webApi.saveSettings(settingsForm)

      if (result.settings) {
        setSettingsData(result.settings)
      }

      if (result.reloginRequired) {
        setAuthenticated(false)
        setGlobalError('设置已保存，API Key 已变更，请使用新 Key 重新登录')
        toast.success('设置已保存，API Key 已变更，请使用新 Key 重新登录')
        return
      }

      const session = await webApi.getSession()
      setProxyAvailable(Boolean(session.proxyAvailable))
      if (!session.proxyAvailable && route === 'proxy') navigate('home')

      toast.success(result.restartRequired ? '设置已保存。端口或监听地址已修改，需要手动重启服务后生效。' : '设置已保存')
    } catch (error) {
      const apiError = error as ApiError
      if (apiError.status === 401 || apiError.status === 403) {
        setAuthenticated(false)
        setGlobalError('登录状态已失效，请重新登录')
      } else {
        toast.error(apiError.message || '保存设置失败')
      }
    } finally {
      setSettingsSaving(false)
    }
  }

  const pageTitle = useMemo<string>(() => {
    if (route === 'codex') return 'Codex'
    if (route === 'xai') return 'xAI'
    if (route === 'proxy') return '代理'
    if (route === 'settings') return '设置'
    return '主页'
  }, [route])

  const pageDescription = useMemo<string>(() => {
    if (route === 'home') return '查看端点概览、实时请求与最近请求。'
    if (route === 'codex') return ''
    if (route === 'xai') return '查看 xAI 账号池、连通测试、auth.json 导入导出与全局配置。'
    if (route === 'proxy') return '管理 Clash/Mihomo 运行状态、订阅、链式代理与节点。'
    return '管理基础设置、全局代理、WebDAV 备份与服务器同步。'
  }, [route])

  if (sessionLoading) {
    return (
      <div className="login-screen">
        <div className="login-card">
          <div className="loading-row">
            <span className="spinner" />
            <span>正在加载 Web 控制台...</span>
          </div>
        </div>
      </div>
    )
  }

  if (!authenticated) {
    return <LoginScreen hasApiKey={hasApiKey} loginKey={loginKey} setLoginKey={setLoginKey} loading={loginLoading} error={globalError} onSubmit={handleLogin} />
  }

  return (
    <div className="app-shell">
      <Toaster />
      <Topbar route={route} proxyAvailable={proxyAvailable} onNavigate={navigate} onLogout={handleLogout} />

      <main className="page">
        <PageHeader
          title={pageTitle}
          description={pageDescription}
          loading={pageLoading}
          onRefresh={() => void refreshCurrentPage(route)}
          showRefresh={route !== 'codex' && route !== 'xai' && route !== 'proxy'}
          extraActions={route === 'home' ? (
            <button type="button" className="btn" onClick={() => setHomeStatsDialogOpen(true)}>
              统计
            </button>
          ) : null}
        />

        {route === 'home' ? (
          <HomePage
            data={homeData}
            loading={pageLoading}
            busyAction={busyAction}
            onOpenEndpointImport={() => setEndpointImportDialogOpen(true)}
            onActivateEndpoint={handleActivateEndpoint}
            onDeleteEndpoint={handleDeleteEndpoint}
          />
        ) : route === 'codex' ? (
          <CodexPage
            data={codexData}
            loading={pageLoading}
            busyAction={busyAction}
            onOpenConfig={handleOpenCodexConfig}
            onOpenImport={handleOpenCodexImport}
            onRefreshCodex={() => refreshCurrentPage('codex')}
            onCopyVisibleAccounts={handleCopyVisibleCodexAccounts}
            onActivateAccount={handleActivateAccount}
            onRefreshToken={handleRefreshCodexToken}
            onFetchUsage={handleFetchCodexUsage}
            onFetchPrimaryUsage={handleFetchCodexPrimaryUsage}
            onResetCredit={handleConsumeCodexResetCredit}
            onCopyAccount={handleCopyCodexAccount}
            onEditAccount={handleOpenCodexEdit}
            onDeleteAccount={handleDeleteCodexAccount}
          />
        ) : route === 'xai' ? (
          <XaiPage
            data={xaiData}
            loading={pageLoading}
            busyAction={busyAction}
            onOpenConfig={handleOpenXaiConfig}
            onOpenImport={() => setXaiImportDialogOpen(true)}
            onOpenSSOImport={() => setXaiSSOImportDialogOpen(true)}
            onRefreshXai={() => refreshCurrentPage('xai')}
            onCopyVisibleAccounts={handleCopyVisibleXaiAccounts}
            onActivateAccount={handleActivateXaiAccount}
            onProbeStream={handleProbeXaiStream}
            onRefreshQuota={handleRefreshXaiQuota}
            onSSO2Auth={handleXaiSSO2Auth}
            onRefreshToken={handleRefreshXaiToken}
            onSetAutoRefreshToken={handleSetXaiAutoRefreshToken}
            onCopyAccount={handleCopyXaiAccount}
            onEditAccount={handleOpenXaiEdit}
            onDeleteAccount={handleDeleteXaiAccount}
          />
        ) : route === 'proxy' ? (
          <ProxyPage
            data={clashData}
            loading={pageLoading}
            onRefresh={() => refreshCurrentPage('proxy')}
            onUnavailable={() => navigate('home')}
            onAuthExpired={handleAuthExpired}
          />
        ) : (
          <SettingsPage
            data={settingsData}
            form={settingsForm}
            loading={pageLoading}
            saving={settingsSaving}
            onChange={setSettingsForm}
            onSubmit={handleSaveSettings}
            onRefreshSettings={() => refreshCurrentPage('settings')}
          />
        )}
      </main>

      <CodexConfigDialog open={codexConfigDialogOpen} form={codexConfigForm} saving={codexConfigSaving} onClose={handleCloseCodexConfig} onChange={setCodexConfigForm} onSubmit={handleSaveCodexConfig} />
      <CodexEditDialog open={codexEditDialogOpen} form={codexEditForm} saving={codexEditSaving} onClose={handleCloseCodexEdit} onChange={setCodexEditForm} onRestore={handleRestoreCodexAccount} onSubmit={handleSaveCodexEdit} />
      <CodexImportDialog open={codexImportDialogOpen} onClose={handleCloseCodexImport} onSuccess={() => refreshCurrentPage('codex')} />
      <XaiConfigDialog open={xaiConfigDialogOpen} form={xaiConfigForm} saving={xaiConfigSaving} onClose={handleCloseXaiConfig} onChange={setXaiConfigForm} onSubmit={handleSaveXaiConfig} />
      <XaiEditDialog open={xaiEditDialogOpen} form={xaiEditForm} saving={xaiEditSaving} onClose={handleCloseXaiEdit} onChange={setXaiEditForm} onSubmit={handleSaveXaiEdit} />
      <XaiImportDialog open={xaiImportDialogOpen} onClose={() => setXaiImportDialogOpen(false)} onImported={() => refreshCurrentPage('xai')} />
      <XaiSSOImportDialog
        open={xaiSSOImportDialogOpen}
        onClose={() => setXaiSSOImportDialogOpen(false)}
        onImported={() => refreshCurrentPage('xai')}
        onAuthExpired={handleAuthExpired}
      />
      <HomeStatsDialog open={homeStatsDialogOpen} onClose={() => setHomeStatsDialogOpen(false)} onCleared={() => refreshCurrentPage('home')} onAuthExpired={handleAuthExpired} />
      <EndpointImportDialog open={endpointImportDialogOpen} onClose={() => setEndpointImportDialogOpen(false)} onSuccess={() => refreshCurrentPage('home')} onAuthExpired={handleAuthExpired} />
    </div>
  )
}
