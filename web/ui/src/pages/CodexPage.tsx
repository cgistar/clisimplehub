import { useEffect, useRef, useState } from 'react'
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
  onDeleteAccounts: (accountIds: string[]) => Promise<boolean>
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
  onDeleteAccounts,
}: CodexPageProps) {
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())
  const batchDeleteBusy = busyAction === 'codex:delete-batch'

  useEffect(() => {
    const existingIds = new Set((data?.accounts || []).map((account) => account.id))
    setSelectedIds((current) => new Set([...current].filter((id) => existingIds.has(id))))
  }, [data?.accounts])

  function toggleAccount(accountId: string) {
    setSelectedIds((current) => {
      const next = new Set(current)
      if (next.has(accountId)) next.delete(accountId)
      else next.add(accountId)
      return next
    })
  }

  async function deleteSelectedAccounts() {
    if (busyAction || loading) return
    const ids = (data?.accounts || []).flatMap((account) => account.id && selectedIds.has(account.id) ? [account.id] : [])
    if (!ids.length || !window.confirm(`确定删除选中的 ${ids.length} 个 Codex 账号吗？删除后无法恢复。`)) return
    if (await onDeleteAccounts(ids)) setSelectedIds(new Set())
  }

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
  const selectableIds = accounts.flatMap((account) => account.id ? [account.id] : [])
  const selectedCount = selectableIds.filter((id) => selectedIds.has(id)).length
  const allSelected = selectableIds.length > 0 && selectedCount === selectableIds.length
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
            <div className="card-subtitle">支持激活、刷新 Token、获取用量、复制、批量删除</div>
          </div>

          <div className="actions codex-account-section-actions">
            <button className="btn" type="button" disabled={Boolean(busyAction) || loading || selectableIds.length === 0} onClick={() => setSelectedIds(allSelected ? new Set() : new Set(selectableIds))}>
              {allSelected ? '取消全选' : '全选'}
            </button>
            <button className="btn danger" type="button" disabled={Boolean(busyAction) || loading || selectedCount === 0} onClick={() => void deleteSelectedAccounts()}>
              {batchDeleteBusy ? '删除中...' : `批量删除 (${selectedCount})`}
            </button>
            <button className="btn" type="button" onClick={onRefreshCodex} disabled={loading || batchDeleteBusy}>
              {loading ? '刷新中...' : '刷新'}
            </button>
            <button className="btn" type="button" onClick={() => onCopyVisibleAccounts(accounts)} disabled={accounts.length === 0}>
              复制
            </button>
            <button className="btn" type="button" onClick={onOpenImport} disabled={batchDeleteBusy}>
              导入
            </button>
            <button className="btn" type="button" onClick={onOpenConfig} disabled={batchDeleteBusy}>
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
                selected={Boolean(account.id && selectedIds.has(account.id))}
                selectionDisabled={Boolean(busyAction) || loading}
                onToggleSelection={toggleAccount}
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
        onRestore={webApi.getDefaultCodexModelPrices}
      />
    </div>
  )
}
