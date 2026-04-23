import { useEffect, useMemo, useState, type FormEvent } from 'react'
import { toast } from 'sonner'
import { ROUTES, routeFromPath } from '@/constants/routes'
import { webApi } from '@/api/web'
import type { ApiError } from '@/api/client'
import { copyToClipboard } from '@/lib/format'
import { createCodexConfigForm, createCodexEditForm } from '@/lib/codex'
import type { ActionResponse, CodexAccount, CodexConfigForm, CodexEditForm, CodexPageData, HomePageData, RouteKey, SettingsData, SettingsForm } from '@/types'
import LoginScreen from '@/components/LoginScreen'
import Topbar from '@/components/Topbar'
import PageHeader from '@/components/PageHeader'
import HomePage from '@/pages/HomePage'
import CodexPage from '@/pages/CodexPage'
import SettingsPage from '@/pages/SettingsPage'
import CodexConfigDialog from '@/pages/components/CodexConfigDialog'
import CodexEditDialog from '@/pages/components/CodexEditDialog'
import CodexImportDialog from '@/pages/components/CodexImportDialog'
import { Toaster } from '@/components/ui/sonner'

export default function App() {
  const [route, setRoute] = useState<RouteKey>(routeFromPath(window.location.pathname))
  const [sessionLoading, setSessionLoading] = useState<boolean>(true)
  const [authenticated, setAuthenticated] = useState<boolean>(false)
  const [hasApiKey, setHasApiKey] = useState<boolean>(true)
  const [loginKey, setLoginKey] = useState<string>('')
  const [loginLoading, setLoginLoading] = useState<boolean>(false)
  const [pageLoading, setPageLoading] = useState<boolean>(false)
  const [globalError, setGlobalError] = useState<string>('')
  const [busyAction, setBusyAction] = useState<string>('')
  const [homeData, setHomeData] = useState<HomePageData | null>(null)
  const [codexData, setCodexData] = useState<CodexPageData | null>(null)
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
    if (!settingsData) return
    setSettingsForm({
      port: settingsData.port || 5600,
      apiKey: settingsData.apiKey || '',
      fallback: Boolean(settingsData.fallback),
      debugMode: settingsData.debugMode || '',
      listenAddr: settingsData.listenAddr || '0.0.0.0',
      proxyUrl: settingsData.proxyUrl || '',
      clashPath: settingsData.clashPath || '',
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
    setSettingsData(null)
    setSettingsForm(null)
    setCodexConfigDialogOpen(false)
    setCodexImportDialogOpen(false)
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

  async function handleDeleteCodexAccount(accountId: string): Promise<void> {
    await runCodexAction(`codex:delete:${accountId}`, () => webApi.deleteCodexAccount(accountId), '账号已删除', '删除 Codex 账号失败')
  }

  async function handleCopyCodexAccount(account: CodexAccount): Promise<void> {
    try {
      const copyData: Record<string, string | number> = {}
      if (account.refreshToken) copyData.refreshToken = account.refreshToken
      if (account.email) copyData.email = account.email
      if (account.accountId) copyData.accountId = account.accountId
      if (account.planType) copyData.planType = account.planType
      if (account.accessToken) copyData.accessToken = account.accessToken
      if (account.idToken) copyData.idToken = account.idToken
      if (account.password) copyData.password = account.password
      if (account.mfaCode) copyData.mfaCode = account.mfaCode
      if (account.expiresAt) copyData.expiresAt = account.expiresAt
      if (account.proxyUrl) copyData.proxyUrl = account.proxyUrl
      if (typeof account.weight === 'number') copyData.weight = account.weight

      await copyToClipboard(JSON.stringify(copyData, null, 2))
      toast.success('账号信息已复制到剪贴板')
    } catch (error) {
      toast.error((error as ApiError).message || '复制账号信息失败')
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
    if (route === 'settings') return '设置'
    return '主页'
  }, [route])

  const pageDescription = useMemo<string>(() => {
    if (route === 'home') return '查看端点概览、实时请求与最近请求。'
    if (route === 'codex') return '查看 Codex 账号池、卡片操作与全局配置。'
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
      <Topbar route={route} onNavigate={navigate} onLogout={handleLogout} />

      <main className="page">
        <PageHeader title={pageTitle} description={pageDescription} loading={pageLoading} onRefresh={() => void refreshCurrentPage(route)} showRefresh={route !== 'codex'} />

        {route === 'home' ? (
          <HomePage
            data={homeData}
            loading={pageLoading}
            busyAction={busyAction}
            onActivateEndpoint={handleActivateEndpoint}
            onRefreshHome={() => refreshCurrentPage('home')}
          />
        ) : route === 'codex' ? (
          <CodexPage
            data={codexData}
            loading={pageLoading}
            busyAction={busyAction}
            onOpenConfig={handleOpenCodexConfig}
            onOpenImport={handleOpenCodexImport}
            onRefreshCodex={() => refreshCurrentPage('codex')}
            onActivateAccount={handleActivateAccount}
            onRefreshToken={handleRefreshCodexToken}
            onFetchUsage={handleFetchCodexUsage}
            onCopyAccount={handleCopyCodexAccount}
            onEditAccount={handleOpenCodexEdit}
            onDeleteAccount={handleDeleteCodexAccount}
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
      <CodexEditDialog open={codexEditDialogOpen} form={codexEditForm} saving={codexEditSaving} onClose={handleCloseCodexEdit} onChange={setCodexEditForm} onSubmit={handleSaveCodexEdit} />
      <CodexImportDialog open={codexImportDialogOpen} onClose={handleCloseCodexImport} onSuccess={() => refreshCurrentPage('codex')} />
    </div>
  )
}
