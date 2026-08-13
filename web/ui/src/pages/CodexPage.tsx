import { useRef, useState } from 'react'
import { toast } from 'sonner'
import KpiCard from '@/components/KpiCard'
import { createCodexConfigForm } from '@/lib/codex'
import { formatTokenCount } from '@/lib/format'
import type { CodexAccount, CodexModelPrice, CodexPageData } from '@/types'
import { webApi } from '@/api/web'
import CodexAccountCard from './components/CodexAccountCard'
import CodexModelPricesDialog from './components/CodexModelPricesDialog'

interface CodexPageProps {
  data: CodexPageData | null
  loading: boolean
  busyAction: string
  onOpenConfig: () => void
  onOpenImport: () => void
  onRefreshCodex: () => void | Promise<void>
  onCopyVisibleAccounts: (accounts: CodexAccount[]) => void | Promise<void>
  onActivateAccount: (accountId: string) => void
  onRefreshToken: (accountId: string) => void
  onFetchUsage: (accountId: string) => void
  onFetchPrimaryUsage: (accountId: string) => void
  onResetCredit: (accountId: string, creditId: string) => void | Promise<void>
  onCopyAccount: (account: CodexAccount) => void
  onEditAccount: (account: CodexAccount) => void
  onDeleteAccount: (accountId: string) => void
}

export default function CodexPage({
  data,
  loading,
  busyAction,
  onOpenConfig,
  onOpenImport,
  onRefreshCodex,
  onCopyVisibleAccounts,
  onActivateAccount,
  onRefreshToken,
  onFetchUsage,
  onFetchPrimaryUsage,
  onResetCredit,
  onCopyAccount,
  onEditAccount,
  onDeleteAccount,
}: CodexPageProps) {
  const [modelPricesOpen, setModelPricesOpen] = useState(false)
  const [modelPrices, setModelPrices] = useState<CodexModelPrice[]>([])
  const [modelPricesLoading, setModelPricesLoading] = useState(false)
  const [modelPricesSaving, setModelPricesSaving] = useState(false)
  const modelPricesLoaded = useRef(false)
  const modelPricesLoadPromise = useRef<Promise<CodexModelPrice[]> | null>(null)

  async function openModelPrices(): Promise<void> {
    setModelPricesOpen(true)
    if (modelPricesLoaded.current) return
    if (!modelPricesLoadPromise.current) {
      setModelPricesLoading(true)
      modelPricesLoadPromise.current = webApi.getCodexModelPrices()
        .then((prices) => {
          setModelPrices(prices)
          modelPricesLoaded.current = true
          return prices
        })
        .finally(() => {
          setModelPricesLoading(false)
          modelPricesLoadPromise.current = null
        })
    }
    try {
      await modelPricesLoadPromise.current
    } catch (error) {
      setModelPricesOpen(false)
      toast.error(error instanceof Error ? error.message : '加载模型单价失败')
    }
  }

  async function saveModelPrices(prices: CodexModelPrice[]): Promise<void> {
    setModelPricesSaving(true)
    try {
      const saved = await webApi.saveCodexModelPrices(prices)
      setModelPrices(saved)
      modelPricesLoaded.current = true
      setModelPricesOpen(false)
      await onRefreshCodex()
      toast.success('模型单价已保存')
    } finally {
      setModelPricesSaving(false)
    }
  }

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
        <div className="kpis codex-kpis">
          <KpiCard label="账号总数" value={accounts.length} />
          <KpiCard label="正常账号" value={validCount} />
          <KpiCard label="冷却账号" value={cooldownCount} />
          <KpiCard label="今日请求" value={totalTodayRequests} />
        </div>

        <div className="list-item-meta mt-16">
          <span className="meta-pill">轮询模式: {globalConfig.rotationMode}</span>
          <span className="meta-pill">Codex URL: {globalConfig.baseURL || '-'}</span>
          <span className="meta-pill">代理URL: {globalConfig.proxyUrl || '-'}</span>
          <span className="meta-pill">今日Token: {formatTokenCount(totalTodayTokens)}</span>
        </div>
      </section>

      <section className="col-12 card">
        <div className="card-header codex-account-section-header">
          <div>
            <h2 className="card-title">账号卡片</h2>
            <div className="card-subtitle">支持激活、刷新 Token、获取用量、复制、删除</div>
          </div>

          <div className="actions codex-account-section-actions">
            <button className="btn" type="button" onClick={onRefreshCodex} disabled={loading}>
              {loading ? '刷新中...' : '刷新'}
            </button>
            <button className="btn" type="button" onClick={() => onCopyVisibleAccounts(accounts)} disabled={accounts.length === 0}>
              复制
            </button>
            <button className="btn" type="button" onClick={onOpenImport}>
              导入
            </button>
            <button className="btn" type="button" onClick={onOpenConfig}>
              配置
            </button>
            <button className="btn" type="button" onClick={() => void openModelPrices()}>
              模型单价
            </button>
          </div>
        </div>

        {accounts.length === 0 ? (
          <div className="empty-state">当前没有 Codex 账号</div>
        ) : (
          <div className="codex-account-grid">
            {accounts.map((account) => (
              <CodexAccountCard
                key={account.id || account.accountId || account.refreshToken}
                account={account}
                busyAction={busyAction}
                onActivate={onActivateAccount}
                onRefreshToken={onRefreshToken}
                onFetchUsage={onFetchUsage}
                onFetchPrimaryUsage={onFetchPrimaryUsage}
                onResetCredit={onResetCredit}
                onCopy={onCopyAccount}
                onEdit={onEditAccount}
                onDelete={onDeleteAccount}
              />
            ))}
          </div>
        )}
      </section>

      <CodexModelPricesDialog
        open={modelPricesOpen}
        prices={modelPrices}
        loading={modelPricesLoading}
        saving={modelPricesSaving}
        onOpenChange={setModelPricesOpen}
        onSave={saveModelPrices}
      />
    </div>
  )
}
