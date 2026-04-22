import KpiCard from '@/components/KpiCard'
import { createCodexConfigForm } from '@/lib/codex'
import { formatTokenCount } from '@/lib/format'
import type { CodexAccount, CodexPageData } from '@/types'
import CodexAccountCard from './components/CodexAccountCard'

interface CodexPageProps {
  data: CodexPageData | null
  loading: boolean
  busyAction: string
  onOpenConfig: () => void
  onActivateAccount: (accountId: string) => void
  onRefreshToken: (accountId: string) => void
  onFetchUsage: (accountId: string) => void
  onCopyAccount: (account: CodexAccount) => void
  onEditAccount: (account: CodexAccount) => void
  onDeleteAccount: (accountId: string) => void
}

export default function CodexPage({
  data,
  loading,
  busyAction,
  onOpenConfig,
  onActivateAccount,
  onRefreshToken,
  onFetchUsage,
  onCopyAccount,
  onEditAccount,
  onDeleteAccount,
}: CodexPageProps) {
  if (loading && !data) return <div className="card empty-state">正在加载 Codex 数据...</div>
  if (!data) return <div className="card empty-state">暂无 Codex 数据</div>
  if (!data.available) {
    return (
      <div className="card">
        <div className="card-header">
          <div>
            <h2 className="card-title">Codex 页面</h2>
            <div className="card-subtitle">当前服务未启用 codex-accounts 插件或账号池尚未初始化</div>
          </div>
        </div>
        <div className="notice">{data.message || 'Codex 插件不可用'}</div>
      </div>
    )
  }

  const accounts = data.accounts || []
  const globalConfig = createCodexConfigForm(data.globalConfig)
  const validCount = accounts.filter((account) => account.status === 'valid').length
  const cooldownCount = accounts.filter((account) => Number(account.cooldownRemaining || 0) > 0).length
  const totalTodayRequests = accounts.reduce((sum, account) => sum + (Number(account.todayRequests) || 0), 0)
  const totalTodayTokens = accounts.reduce((sum, account) => sum + (Number(account.todayTotalTokens) || 0), 0)

  return (
    <div className="grid">
      <section className="col-12 card">
        <div className="card-header">
          <div>
            <h2 className="card-title">Codex 账号池</h2>
            <div className="card-subtitle">当前活跃账号：{data.activeAccountId || '未设置'} · {accounts.length} 个账号</div>
          </div>

          <div className="actions">
            <button className="btn" type="button" onClick={onOpenConfig}>
              配置
            </button>
          </div>
        </div>

        <div className="kpis codex-kpis">
          <KpiCard label="账号总数" value={accounts.length} />
          <KpiCard label="正常账号" value={validCount} />
          <KpiCard label="冷却账号" value={cooldownCount} />
          <KpiCard label="今日请求" value={totalTodayRequests} />
        </div>

        <div className="list-item-meta mt-16">
          <span className="meta-pill">rotationMode: {globalConfig.rotationMode}</span>
          <span className="meta-pill">baseURL: {globalConfig.baseURL || '-'}</span>
          <span className="meta-pill">proxyUrl: {globalConfig.proxyUrl || '-'}</span>
          <span className="meta-pill">todayTokens: {formatTokenCount(totalTodayTokens)}</span>
        </div>
      </section>

      <section className="col-12 card">
        <div className="card-header">
          <div>
            <h2 className="card-title">账号卡片</h2>
            <div className="card-subtitle">支持激活、刷新 Token、获取用量、复制、删除</div>
          </div>
        </div>

        {accounts.length === 0 ? (
          <div className="empty-state">当前没有 Codex 账号</div>
        ) : (
          <div className="codex-account-grid">
            {accounts.map((account) => (
              <CodexAccountCard
                key={account.accountId || account.refreshToken}
                account={account}
                busyAction={busyAction}
                onActivate={onActivateAccount}
                onRefreshToken={onRefreshToken}
                onFetchUsage={onFetchUsage}
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
