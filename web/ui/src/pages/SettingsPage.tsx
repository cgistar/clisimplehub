import { useEffect, useMemo, useState, type FormEvent } from 'react'
import { toast } from 'sonner'
import MetaRow from '@/components/MetaRow'
import { webApi } from '@/api/web'
import type { ApiError } from '@/api/client'
import { copyToClipboard, maskSecret } from '@/lib/format'
import type { BackupData, RestoreMode, ServerConfig, SettingsData, SettingsForm, WebDAVBackupItem, WebDAVConfig } from '@/types'

const BACKUP_DIR = '/clisimplehub'

interface SettingsPageProps {
  data: SettingsData | null
  form: SettingsForm | null
  loading: boolean
  saving: boolean
  onChange: (form: SettingsForm) => void
  onSubmit: (event: FormEvent<HTMLFormElement>) => void | Promise<void>
  onRefreshSettings: () => void | Promise<void>
}

function toErrorMessage(error: unknown): string {
  const apiError = error as ApiError
  return apiError?.message || (error instanceof Error ? error.message : String(error))
}

function parseWebDAVBackupsXml(xmlText: string): WebDAVBackupItem[] {
  const parser = new DOMParser()
  const xml = parser.parseFromString(xmlText, 'text/xml')
  const responses = xml.querySelectorAll('response')

  const parsedBackups: WebDAVBackupItem[] = []
  responses.forEach((resp, index) => {
    if (index === 0) return

    const href = resp.querySelector('href')?.textContent || ''
    const displayName = resp.querySelector('displayname')?.textContent || ''
    const lastModified = resp.querySelector('getlastmodified')?.textContent || ''

    let filename = ''
    if (href) {
      filename = decodeURIComponent(href.split('/').filter(Boolean).pop() || '')
    }
    if (!filename && displayName) {
      filename = displayName
    }
    if (!filename.endsWith('.json')) return

    parsedBackups.push({
      filename,
      displayName: displayName || filename,
      href: href || undefined,
      lastModified: lastModified || undefined,
      name: filename.replace('.json', ''),
    })
  })

  return parsedBackups.sort((left, right) => {
    const leftTime = Date.parse(left.lastModified || '') || 0
    const rightTime = Date.parse(right.lastModified || '') || 0
    return rightTime - leftTime
  })
}

function formatBackupTime(raw?: string): string {
  if (!raw) return '-'
  const date = new Date(raw)
  if (Number.isFinite(date.getTime())) return date.toLocaleString('zh-CN', { hour12: false })
  return raw
}

export default function SettingsPage({ data, form, loading, saving, onChange, onSubmit, onRefreshSettings }: SettingsPageProps) {
  const [webdavForm, setWebdavForm] = useState<WebDAVConfig>({ serverUrl: '', username: '', password: '' })
  const [backups, setBackups] = useState<WebDAVBackupItem[]>([])
  const [servers, setServers] = useState<ServerConfig[]>([])
  const [loadingExtras, setLoadingExtras] = useState<boolean>(false)
  const [testingWebdav, setTestingWebdav] = useState<boolean>(false)
  const [savingWebdav, setSavingWebdav] = useState<boolean>(false)
  const [loadingBackups, setLoadingBackups] = useState<boolean>(false)
  const [backingUp, setBackingUp] = useState<boolean>(false)
  const [restoringFilename, setRestoringFilename] = useState<string>('')
  const [deletingFilename, setDeletingFilename] = useState<string>('')
  const [savingServer, setSavingServer] = useState<boolean>(false)
  const [testingServer, setTestingServer] = useState<boolean>(false)
  const [syncingServer, setSyncingServer] = useState<boolean>(false)
  const [copyingServerCurl, setCopyingServerCurl] = useState<boolean>(false)
  const [selectedServerIndex, setSelectedServerIndex] = useState<number>(-1)
  const [serverDialogOpen, setServerDialogOpen] = useState<boolean>(false)
  const [editingServerIndex, setEditingServerIndex] = useState<number>(-1)
  const [serverForm, setServerForm] = useState<ServerConfig>({ name: '', url: '', apiKey: '' })
  const [restoreTarget, setRestoreTarget] = useState<WebDAVBackupItem | null>(null)

  useEffect(() => {
    if (!data) return
    void loadExtras()
  }, [data?.configPath])

  const hasSelectedServer = selectedServerIndex >= 0 && selectedServerIndex < servers.length
  const selectedServer = hasSelectedServer ? servers[selectedServerIndex] : null
  const serverDialogTitle = editingServerIndex >= 0 ? '编辑同步服务器' : '新增同步服务器'
  const busyGeneral = loading || loadingExtras
  const loadingServers = loadingExtras

  const canSubmitGeneral = useMemo(() => {
    if (!form) return false
    const port = Number(form.port)
    return Number.isInteger(port) && port >= 1 && port <= 65535
  }, [form])

  async function loadExtras(): Promise<void> {
    setLoadingExtras(true)
    try {
      const [webdav, nextServers] = await Promise.all([
        webApi.getWebDAVConfig(),
        webApi.getServers(),
      ])
      setWebdavForm({
        serverUrl: webdav.serverUrl || '',
        username: webdav.username || '',
        password: webdav.password || '',
      })
      setServers(nextServers || [])
      setSelectedServerIndex((current) => {
        if (!nextServers || nextServers.length === 0) return -1
        if (current >= 0 && current < nextServers.length) return current
        return 0
      })
      if (webdav.serverUrl) {
        await loadBackupsList({
          serverUrl: webdav.serverUrl || '',
          username: webdav.username || '',
          password: webdav.password || '',
        })
      } else {
        setBackups([])
      }
    } catch (error) {
      toast.error(toErrorMessage(error) || '加载设置扩展数据失败')
    } finally {
      setLoadingExtras(false)
    }
  }

  async function loadBackupsList(configOverride?: WebDAVConfig): Promise<void> {
    const activeConfig = configOverride || webdavForm
    if (!activeConfig.serverUrl.trim()) {
      setBackups([])
      return
    }

    setLoadingBackups(true)
    try {
      const result = await webApi.webdavList({
        config: activeConfig,
        path: BACKUP_DIR,
        depth: '1',
      })
      if (result.error || (result.statusCode !== 200 && result.statusCode !== 207) || !result.body) {
        setBackups([])
        return
      }
      setBackups(parseWebDAVBackupsXml(result.body))
    } catch (error) {
      setBackups([])
      toast.error(`加载 WebDAV 备份列表失败：${toErrorMessage(error)}`)
    } finally {
      setLoadingBackups(false)
    }
  }

  async function ensureBackupDir(config: WebDAVConfig): Promise<boolean> {
    const result = await webApi.webdavMkcol({
      config,
      path: BACKUP_DIR,
    })
    return result.statusCode === 200 || result.statusCode === 201 || result.statusCode === 405
  }

  async function handleTestWebDAVConnection(): Promise<void> {
    if (!webdavForm.serverUrl.trim()) {
      toast.error('请先输入 WebDAV Server URL')
      return
    }
    setTestingWebdav(true)
    try {
      const result = await webApi.testWebDAVConnection(webdavForm)
      if (result.error) {
        toast.error(`WebDAV 连接测试失败：${result.error}`)
        return
      }
      if (result.statusCode === 200 || result.statusCode === 207) {
        toast.success('WebDAV 连接成功')
        return
      }
      toast.error(`WebDAV 连接测试失败：${result.statusCode}`)
    } catch (error) {
      toast.error(`WebDAV 连接测试失败：${toErrorMessage(error)}`)
    } finally {
      setTestingWebdav(false)
    }
  }

  async function handleSaveWebDAVConfig(): Promise<void> {
    if (!webdavForm.serverUrl.trim()) {
      toast.error('请先输入 WebDAV Server URL')
      return
    }
    setSavingWebdav(true)
    try {
      await webApi.saveWebDAVConfig({
        serverUrl: webdavForm.serverUrl.trim(),
        username: webdavForm.username.trim(),
        password: webdavForm.password,
      })
      toast.success('WebDAV 配置已保存')
      await loadBackupsList({
        serverUrl: webdavForm.serverUrl.trim(),
        username: webdavForm.username.trim(),
        password: webdavForm.password,
      })
    } catch (error) {
      toast.error(`保存 WebDAV 配置失败：${toErrorMessage(error)}`)
    } finally {
      setSavingWebdav(false)
    }
  }

  async function handleBackupToWebDAV(): Promise<void> {
    if (!webdavForm.serverUrl.trim()) {
      toast.error('请先输入 WebDAV Server URL')
      return
    }
    setBackingUp(true)
    try {
      const config: WebDAVConfig = {
        serverUrl: webdavForm.serverUrl.trim(),
        username: webdavForm.username.trim(),
        password: webdavForm.password,
      }
      const dirReady = await ensureBackupDir(config)
      if (!dirReady) {
        throw new Error('failed_to_create_backup_dir')
      }

      const backupResponse = await webApi.createBackupData()
      if (!backupResponse.filename || !backupResponse.data) {
        throw new Error('invalid_backup_response')
      }

      const uploadResult = await webApi.webdavPut({
        config,
        path: `${BACKUP_DIR}/${backupResponse.filename}`,
        body: JSON.stringify(backupResponse.data, null, 2),
      })
      if (uploadResult.error) {
        throw new Error(uploadResult.error)
      }
      if (uploadResult.statusCode < 200 || uploadResult.statusCode >= 300) {
        throw new Error(String(uploadResult.statusCode))
      }

      await webApi.saveWebDAVConfig(config)
      toast.success(`备份已上传：${backupResponse.filename}`)
      await loadBackupsList(config)
    } catch (error) {
      toast.error(`备份到 WebDAV 失败：${toErrorMessage(error)}`)
    } finally {
      setBackingUp(false)
    }
  }

  async function confirmRestoreBackup(mode: RestoreMode): Promise<void> {
    if (!restoreTarget) return
    if (!webdavForm.serverUrl.trim()) {
      toast.error('请先输入 WebDAV Server URL')
      return
    }

    setRestoringFilename(restoreTarget.filename)
    try {
      const config: WebDAVConfig = {
        serverUrl: webdavForm.serverUrl.trim(),
        username: webdavForm.username.trim(),
        password: webdavForm.password,
      }
      const result = await webApi.webdavGet({
        config,
        path: `${BACKUP_DIR}/${restoreTarget.filename}`,
      })
      if (result.error) {
        throw new Error(result.error)
      }
      if (result.statusCode !== 200 || !result.body) {
        throw new Error(String(result.statusCode))
      }

      const backupData = JSON.parse(result.body) as BackupData
      await webApi.restoreBackupData(backupData, mode)
      toast.success(mode === 'replace' ? '备份已按替换模式恢复' : '备份已按合并模式恢复')
      await onRefreshSettings()
      setRestoreTarget(null)
    } catch (error) {
      toast.error(`恢复备份失败：${toErrorMessage(error)}`)
    } finally {
      setRestoringFilename('')
    }
  }

  async function handleDeleteBackup(backup: WebDAVBackupItem): Promise<void> {
    if (!webdavForm.serverUrl.trim()) {
      toast.error('请先输入 WebDAV Server URL')
      return
    }
    if (!window.confirm(`确定删除备份 ${backup.filename} 吗？`)) {
      return
    }
    setDeletingFilename(backup.filename)
    try {
      const config: WebDAVConfig = {
        serverUrl: webdavForm.serverUrl.trim(),
        username: webdavForm.username.trim(),
        password: webdavForm.password,
      }
      const result = await webApi.webdavDelete({
        config,
        path: `${BACKUP_DIR}/${backup.filename}`,
      })
      if (result.error) {
        throw new Error(result.error)
      }
      if (result.statusCode < 200 || result.statusCode >= 300) {
        throw new Error(String(result.statusCode))
      }
      toast.success('备份已删除')
      await loadBackupsList(config)
    } catch (error) {
      toast.error(`删除备份失败：${toErrorMessage(error)}`)
    } finally {
      setDeletingFilename('')
    }
  }

  function openAddServerDialog(): void {
    setEditingServerIndex(-1)
    setServerForm({ name: '', url: '', apiKey: '' })
    setServerDialogOpen(true)
  }

  function openEditServerDialog(): void {
    if (!selectedServer) return
    setEditingServerIndex(selectedServerIndex)
    setServerForm({
      name: selectedServer.name || '',
      url: selectedServer.url,
      apiKey: selectedServer.apiKey || '',
    })
    setServerDialogOpen(true)
  }

  async function handleSaveServer(): Promise<void> {
    const normalizedUrl = serverForm.url.trim()
    if (!normalizedUrl) {
      toast.error('请先输入同步服务器 URL')
      return
    }

    setSavingServer(true)
    try {
      const nextEntry: ServerConfig = {
        name: (serverForm.name || '').trim(),
        url: normalizedUrl,
        apiKey: (serverForm.apiKey || '').trim(),
      }
      const nextServers = [...servers]
      const nextIndex = editingServerIndex >= 0 ? editingServerIndex : nextServers.length
      if (editingServerIndex >= 0) {
        nextServers[editingServerIndex] = nextEntry
      } else {
        nextServers.push(nextEntry)
      }
      await webApi.saveServers(nextServers)
      setServers(nextServers)
      setSelectedServerIndex(nextIndex)
      setServerDialogOpen(false)
      toast.success('同步服务器已保存')
    } catch (error) {
      toast.error(`保存同步服务器失败：${toErrorMessage(error)}`)
    } finally {
      setSavingServer(false)
    }
  }

  async function handleDeleteServer(): Promise<void> {
    if (!selectedServer) return
    if (!window.confirm(`确定删除同步服务器 ${selectedServer.name || selectedServer.url} 吗？`)) {
      return
    }
    try {
      const nextServers = servers.filter((_, index) => index !== selectedServerIndex)
      await webApi.saveServers(nextServers)
      setServers(nextServers)
      setSelectedServerIndex(nextServers.length === 0 ? -1 : Math.min(selectedServerIndex, nextServers.length - 1))
      toast.success('同步服务器已删除')
    } catch (error) {
      toast.error(`删除同步服务器失败：${toErrorMessage(error)}`)
    }
  }

  async function handleTestServer(): Promise<void> {
    if (!selectedServer) return
    setTestingServer(true)
    try {
      await webApi.testServerConnection(selectedServer.url, selectedServer.apiKey || '')
      toast.success('同步服务器连接成功')
    } catch (error) {
      toast.error(`测试同步服务器失败：${toErrorMessage(error)}`)
    } finally {
      setTestingServer(false)
    }
  }

  async function handleSyncServer(): Promise<void> {
    if (!selectedServer) return
    if (!window.confirm(`确定同步配置到 ${selectedServer.name || selectedServer.url} 吗？`)) {
      return
    }
    setSyncingServer(true)
    try {
      await webApi.syncConfigToServer(selectedServerIndex)
      toast.success('配置同步成功')
    } catch (error) {
      toast.error(`配置同步失败：${toErrorMessage(error)}`)
    } finally {
      setSyncingServer(false)
    }
  }

  async function handleCopyServerCurl(): Promise<void> {
    if (!selectedServer) return
    setCopyingServerCurl(true)
    try {
      const result = await webApi.buildSyncConfigCurl(selectedServerIndex)
      await copyToClipboard(result.command)
      toast.success('同步 curl 命令已复制')
    } catch (error) {
      toast.error(`复制同步 curl 失败：${toErrorMessage(error)}`)
    } finally {
      setCopyingServerCurl(false)
    }
  }

  if (busyGeneral && !form) return <div className="card empty-state">正在加载设置...</div>
  if (!data || !form) return <div className="card empty-state">暂无设置数据</div>

  const updateField = <K extends keyof SettingsForm>(key: K, value: SettingsForm[K]) => onChange({ ...form, [key]: value })

  return (
    <>
      <div className="grid settings-grid">
        <section className="col-12 card">
          <div className="card-header">
            <div>
              <h2 className="card-title">基础设置</h2>
              <div className="card-subtitle">对齐桌面版：支持监听、鉴权、调试、Fallback 与全局代理 URL</div>
            </div>
          </div>

          <form onSubmit={onSubmit}>
            <div className="field-row">
              <div className="field">
                <label className="field-label">Port</label>
                <input className="input" type="number" min="1" max="65535" value={form.port} onChange={(event) => updateField('port', event.target.value)} />
              </div>

              <div className="field">
                <label className="field-label">Listen Address</label>
                <input className="input" value={form.listenAddr} onChange={(event) => updateField('listenAddr', event.target.value)} placeholder="0.0.0.0" />
              </div>
            </div>

            <div className="field-row mt-14">
              <div className="field">
                <label className="field-label">API Key</label>
                <input className="input" type="password" value={form.apiKey} onChange={(event) => updateField('apiKey', event.target.value)} placeholder="为空表示关闭 API 登录认证" />
              </div>

              <div className="field">
                <label className="field-label">Proxy URL</label>
                <input className="input" value={form.proxyUrl} onChange={(event) => updateField('proxyUrl', event.target.value)} placeholder="例如：socks5://127.0.0.1:1080" />
              </div>
            </div>

            <div className="field mt-14">
              <label className="field-label">Debug Mode</label>
              <select className="select" value={form.debugMode} onChange={(event) => updateField('debugMode', event.target.value)}>
                <option value="">关闭</option>
                <option value="db">db</option>
                <option value="file">file</option>
              </select>
            </div>

            <div className="switch-row mt-14">
              <div>
                <div>Fallback</div>
                <div className="muted small">开启后，部分请求在无匹配路由时会走 fallback 逻辑</div>
              </div>
              <input className="checkbox" type="checkbox" checked={!!form.fallback} onChange={(event) => updateField('fallback', event.target.checked)} />
            </div>

            <div className="actions mt-18">
              <button type="submit" className="btn primary" disabled={saving || !canSubmitGeneral}>
                {saving ? '保存中...' : '保存基础设置'}
              </button>
            </div>
          </form>
        </section>

        <section className="col-12 card">
          <div className="card-header">
            <div>
              <h2 className="card-title">WebDAV 配置</h2>
              <div className="card-subtitle">用于配置备份、恢复与远程存档管理</div>
            </div>
          </div>

          <div className="field-row">
            <div className="field field-span-2">
              <label className="field-label">Server URL</label>
              <div className="inline-field-action">
                <input className="input" value={webdavForm.serverUrl} onChange={(event) => setWebdavForm((current) => ({ ...current, serverUrl: event.target.value }))} placeholder="https://dav.example.com/remote.php/dav/files/user" />
                <button type="button" className="btn" disabled={testingWebdav} onClick={() => void handleTestWebDAVConnection()}>
                  {testingWebdav ? '测试中...' : '测试连接'}
                </button>
              </div>
            </div>

            <div className="field">
              <label className="field-label">Username</label>
              <input className="input" value={webdavForm.username} onChange={(event) => setWebdavForm((current) => ({ ...current, username: event.target.value }))} placeholder="用户名" />
            </div>

            <div className="field">
              <label className="field-label">Password</label>
              <input className="input" type="password" value={webdavForm.password} onChange={(event) => setWebdavForm((current) => ({ ...current, password: event.target.value }))} placeholder="密码 / App Password" />
            </div>
          </div>

          <div className="actions mt-18">
            <button type="button" className="btn primary" disabled={savingWebdav} onClick={() => void handleSaveWebDAVConfig()}>
              {savingWebdav ? '保存中...' : '保存 WebDAV 配置'}
            </button>
          </div>
        </section>

        <section className="col-12 card">
          <div className="card-header">
            <div>
              <h2 className="card-title">WebDAV 备份</h2>
              <div className="card-subtitle">支持刷新列表、备份到远程、按 merge/replace 恢复、删除备份</div>
            </div>
            <div className="actions">
              <button type="button" className="btn" disabled={loadingBackups} onClick={() => void loadBackupsList()}>
                {loadingBackups ? '刷新中...' : '刷新列表'}
              </button>
              <button type="button" className="btn primary" disabled={backingUp} onClick={() => void handleBackupToWebDAV()}>
                {backingUp ? '备份中...' : '立即备份'}
              </button>
            </div>
          </div>

          {loadingBackups ? <div className="empty-state">正在加载备份列表...</div> : null}
          {!loadingBackups && backups.length === 0 ? <div className="empty-state">当前没有 WebDAV 备份</div> : null}
          {!loadingBackups && backups.length > 0 ? (
            <div className="backup-list">
              {backups.map((backup) => (
                <div className="backup-item" key={backup.filename}>
                  <div className="backup-meta">
                    <div className="backup-name">{backup.displayName}</div>
                    <div className="backup-time">{formatBackupTime(backup.lastModified)}</div>
                  </div>
                  <div className="actions">
                    <button type="button" className="btn" disabled={restoringFilename === backup.filename} onClick={() => setRestoreTarget(backup)}>
                      {restoringFilename === backup.filename ? '恢复中...' : '恢复'}
                    </button>
                    <button type="button" className="btn danger" disabled={deletingFilename === backup.filename} onClick={() => void handleDeleteBackup(backup)}>
                      {deletingFilename === backup.filename ? '删除中...' : '删除'}
                    </button>
                  </div>
                </div>
              ))}
            </div>
          ) : null}
        </section>

        <section className="col-12 card">
          <div className="card-header">
            <div>
              <h2 className="card-title">服务器同步</h2>
              <div className="card-subtitle">管理远端 headless server，同步 vendors / endpoints / 插件配置</div>
            </div>
          </div>

          <div className="server-toolbar">
            <select className="select server-select" value={selectedServerIndex} disabled={loadingServers || servers.length === 0} onChange={(event) => setSelectedServerIndex(Number(event.target.value))}>
              {servers.length === 0 ? <option value={-1}>暂无同步服务器</option> : null}
              {servers.map((server, index) => (
                <option key={`${server.url}-${index}`} value={index}>
                  {server.name || server.url}
                </option>
              ))}
            </select>

            <div className="actions">
              <button type="button" className="btn" onClick={openAddServerDialog}>新增</button>
              <button type="button" className="btn" disabled={!selectedServer} onClick={openEditServerDialog}>编辑</button>
              <button type="button" className="btn" disabled={!selectedServer || testingServer} onClick={() => void handleTestServer()}>
                {testingServer ? '测试中...' : '测试连接'}
              </button>
              <button type="button" className="btn danger" disabled={!selectedServer} onClick={() => void handleDeleteServer()}>删除</button>
            </div>
          </div>

          {selectedServer ? (
            <div className="meta-list mt-16">
              <MetaRow label="名称" value={selectedServer.name || '-'} />
              <MetaRow label="URL" value={selectedServer.url} />
              <MetaRow label="API Key" value={maskSecret(selectedServer.apiKey || '')} />
            </div>
          ) : (
            <div className="empty-state">请先新增一个同步服务器</div>
          )}

          <div className="actions mt-18">
            <button type="button" className="btn" disabled={!selectedServer || copyingServerCurl} onClick={() => void handleCopyServerCurl()}>
              {copyingServerCurl ? '复制中...' : '复制 curl'}
            </button>
            <button type="button" className="btn primary" disabled={!selectedServer || syncingServer} onClick={() => void handleSyncServer()}>
              {syncingServer ? '同步中...' : '同步配置'}
            </button>
          </div>
        </section>
      </div>

      {serverDialogOpen ? (
        <div className="dialog-backdrop" onClick={() => !savingServer && setServerDialogOpen(false)}>
          <div className="dialog-card dialog-card-narrow" onClick={(event) => event.stopPropagation()}>
            <div className="card-header">
              <div>
                <h2 className="card-title">{serverDialogTitle}</h2>
                <div className="card-subtitle">用于 /sync/config 的远端服务器信息</div>
              </div>
              <button type="button" className="btn" disabled={savingServer} onClick={() => setServerDialogOpen(false)}>
                关闭
              </button>
            </div>

            <div className="field">
              <label className="field-label">名称</label>
              <input className="input" value={serverForm.name || ''} onChange={(event) => setServerForm((current) => ({ ...current, name: event.target.value }))} placeholder="可选：机器名 / 环境名" />
            </div>

            <div className="field mt-14">
              <label className="field-label">URL</label>
              <input className="input" value={serverForm.url} onChange={(event) => setServerForm((current) => ({ ...current, url: event.target.value }))} placeholder="http://127.0.0.1:5600" />
            </div>

            <div className="field mt-14">
              <label className="field-label">API Key</label>
              <input className="input" type="password" value={serverForm.apiKey || ''} onChange={(event) => setServerForm((current) => ({ ...current, apiKey: event.target.value }))} placeholder="可选：远端服务鉴权 Key" />
            </div>

            <div className="actions mt-18 dialog-actions">
              <button type="button" className="btn" disabled={savingServer} onClick={() => setServerDialogOpen(false)}>
                取消
              </button>
              <button type="button" className="btn primary" disabled={savingServer} onClick={() => void handleSaveServer()}>
                {savingServer ? '保存中...' : '保存'}
              </button>
            </div>
          </div>
        </div>
      ) : null}

      {restoreTarget ? (
        <div className="dialog-backdrop" onClick={() => !restoringFilename && setRestoreTarget(null)}>
          <div className="dialog-card dialog-card-narrow" onClick={(event) => event.stopPropagation()}>
            <div className="card-header">
              <div>
                <h2 className="card-title">恢复备份</h2>
                <div className="card-subtitle">选择恢复模式：merge 保留本地数据，replace 以备份覆盖本地</div>
              </div>
              <button type="button" className="btn" disabled={!!restoringFilename} onClick={() => setRestoreTarget(null)}>
                关闭
              </button>
            </div>

            <div className="notice">
              目标备份：<strong>{restoreTarget.filename}</strong>
            </div>

            <div className="actions mt-18 dialog-actions">
              <button type="button" className="btn" disabled={!!restoringFilename} onClick={() => setRestoreTarget(null)}>
                取消
              </button>
              <button type="button" className="btn" disabled={!!restoringFilename} onClick={() => void confirmRestoreBackup('merge')}>
                {restoringFilename ? '恢复中...' : '合并恢复'}
              </button>
              <button type="button" className="btn danger" disabled={!!restoringFilename} onClick={() => void confirmRestoreBackup('replace')}>
                {restoringFilename ? '恢复中...' : '替换恢复'}
              </button>
            </div>
          </div>
        </div>
      ) : null}
    </>
  )
}
