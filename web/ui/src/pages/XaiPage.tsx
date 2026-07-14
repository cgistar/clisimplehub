import type { XaiAccount, XaiPageData } from '@/types'
import { createXaiConfigForm } from '@/lib/xai'
import KpiCard from '@/components/KpiCard'
import XaiAccountCard from './components/XaiAccountCard'
import { Menu } from '@base-ui/react/menu'

interface XaiPageProps {
  data: XaiPageData | null
  loading: boolean
  busyAction: string
  onOpenConfig: () => void
  onOpenImport: () => void
  onOpenSSOImport: () => void
  onRefreshXai: () => void | Promise<void>
  onCopyVisibleAccounts: (accounts: XaiAccount[]) => void | Promise<void>
  onActivateAccount: (accountId: string) => void
  onProbeStream: (accountId: string) => void
  onRefreshQuota: (accountId: string) => void
  onSSO2Auth: (accountId: string) => void
  onRefreshToken: (accountId: string) => void
  onSetAutoRefreshToken: (enabled: boolean) => void
  onCopyAccount: (account: XaiAccount) => void
  onEditAccount: (account: XaiAccount) => void
  onDeleteAccount: (accountId: string) => void
}

export default function XaiPage({
  data,
  loading,
  busyAction,
  onOpenConfig,
  onOpenImport,
  onOpenSSOImport,
  onRefreshXai,
  onCopyVisibleAccounts,
  onActivateAccount,
  onProbeStream,
  onRefreshQuota,
  onSSO2Auth,
  onRefreshToken,
  onSetAutoRefreshToken,
  onCopyAccount,
  onEditAccount,
  onDeleteAccount,
}: XaiPageProps) {
  if (loading && !data) return <div className="card empty-state">正在加载 xAI 数据...</div>
  if (!data) return <div className="card empty-state">暂无 xAI 数据</div>
  if (!data.available) {
    return (
      <div className="card">
        <div className="card-header">
          <div>
            <h2 className="card-title">xAI 页面</h2>
            <div className="card-subtitle">当前服务未启用 xai-accounts 插件或账号池尚未初始化</div>
          </div>
        </div>
        {data.message ? <div className="muted">{data.message}</div> : null}
      </div>
    )
  }

  const accounts = data.accounts || []
  const globalConfig = createXaiConfigForm(data.globalConfig)
  const validCount = accounts.filter((a) => a.status === 'valid' || !a.status).length
  const bannedCount = accounts.filter((a) => a.status === 'banned').length
  const exhaustedCount = accounts.filter((a) => a.status === 'exhausted').length
  const cooldownCount = accounts.filter((a) => Number(a.cooldownRemaining || 0) > 0).length

  return (
    <div className="grid">
      <section className="col-12 card">
        <div className="kpis codex-kpis">
          <KpiCard label="账号总数" value={accounts.length} />
          <KpiCard label="正常账号" value={validCount} />
          <KpiCard label="封禁/用尽" value={bannedCount + exhaustedCount} />
          <KpiCard label="冷却账号" value={cooldownCount} />
        </div>
        <div className="list-item-meta mt-16">
          <span className="meta-pill">轮询模式: {globalConfig.rotationMode}</span>
          <span className="meta-pill">Base URL: {globalConfig.baseURL || '-'}</span>
          <span className="meta-pill">代理URL: {globalConfig.proxyUrl || '-'}</span>
        </div>
      </section>

      <section className="col-12 card">
        <div className="card-header codex-account-section-header">
          <div>
            <h2 className="card-title">账号卡片</h2>
            <div className="card-subtitle">支持激活、连通测试、刷新额度、SSO2Auth、刷新 Token、复制 auth.json、导入、删除</div>
          </div>
          <div className="actions codex-account-section-actions">
            <label className="checkbox-row" title="每分钟检查并刷新 5 分钟内到期的已启用 OAuth 账号">
              <input
                type="checkbox"
                checked={globalConfig.autoRefreshToken}
                disabled={busyAction === 'xai:auto-refresh'}
                onChange={(event) => onSetAutoRefreshToken(event.target.checked)}
              />
              <span>{busyAction === 'xai:auto-refresh' ? '保存中...' : '自动更新 Token'}</span>
            </label>
            <button className="btn" type="button" onClick={onRefreshXai} disabled={loading}>
              {loading ? '刷新中...' : '刷新'}
            </button>
            <button className="btn" type="button" onClick={() => onCopyVisibleAccounts(accounts)} disabled={accounts.length === 0}>
              复制
            </button>
            <Menu.Root>
              <Menu.Trigger className="btn" type="button">账号</Menu.Trigger>
              <Menu.Portal>
                <Menu.Positioner className="account-menu-positioner" sideOffset={8} align="end">
                  <Menu.Popup className="account-menu-popup">
                    <Menu.Item className="account-menu-item" onClick={onOpenImport}>JSON 导入</Menu.Item>
                    <Menu.Item className="account-menu-item" onClick={onOpenSSOImport}>SSO 导入</Menu.Item>
                  </Menu.Popup>
                </Menu.Positioner>
              </Menu.Portal>
            </Menu.Root>
            <button className="btn" type="button" onClick={onOpenConfig}>
              配置
            </button>
          </div>
        </div>

        {accounts.length === 0 ? (
          <div className="empty-state">当前没有 xAI 账号</div>
        ) : (
          <div className="codex-account-grid">
            {accounts.map((account) => (
              <XaiAccountCard
                key={account.id || account.email || account.subject}
                account={account}
                busyAction={busyAction}
                onActivate={onActivateAccount}
                onProbeStream={onProbeStream}
                onRefreshQuota={onRefreshQuota}
                onSSO2Auth={onSSO2Auth}
                onRefreshToken={onRefreshToken}
                onCopy={onCopyAccount}
                onEdit={onEditAccount}
                onDelete={onDeleteAccount}
              />
            ))}
          </div>
        )}
      </section>
    </div>
  )
}
