import { useEffect, useMemo, useState } from 'react'
import { ClipboardCopy, Globe, Pencil, Play, RefreshCw, Route, Settings2, Square, Trash2, Wifi, WifiOff } from 'lucide-react'
import { toast } from 'sonner'
import { webApi } from '@/api/web'
import type { ApiError } from '@/api/client'
import { copyToClipboard } from '@/lib/format'
import type { ClashConfig, ClashPageData, ClashSubscription } from '@/types'
import ClashConfigDialog from './components/ClashConfigDialog'
import ClashSubscriptionDialog from './components/ClashSubscriptionDialog'
import ClashNodesDialog from './components/ClashNodesDialog'

interface Props { data: ClashPageData | null; loading: boolean; onRefresh: () => void | Promise<void>; onUnavailable: () => void; onAuthExpired: () => void }

export default function ProxyPage({ data, loading, onRefresh, onUnavailable, onAuthExpired }: Props) {
  const [busy, setBusy] = useState('')
  const [configOpen, setConfigOpen] = useState(false)
  const [subscriptionOpen, setSubscriptionOpen] = useState(false)
  const [editing, setEditing] = useState<ClashSubscription | null>(null)
  const [nodesSubscription, setNodesSubscription] = useState<ClashSubscription | null>(null)
  const config = data?.config
  const status = data?.status
  const subscriptions = config?.subscriptions || []
  const chainID = useMemo(() => String(config?.chain?.exit?.subscriptionId || config?.dialerProxyId || ''), [config])

  useEffect(() => { if (data && !data.available) onUnavailable() }, [data, onUnavailable])

  async function run(key: string, task: () => Promise<unknown>, success: string, refresh = true) {
    setBusy(key)
    try { await task(); toast.success(success); if (refresh) await onRefresh() }
    catch (error) { const apiError = error as ApiError; if (apiError.status === 401 || apiError.status === 403) onAuthExpired(); else toast.error(error instanceof Error ? error.message : '操作失败') }
    finally { setBusy('') }
  }

  if (loading && !data) return <div className="card empty-state">正在加载代理数据...</div>
  if (!data) return <div className="card empty-state">暂无代理数据</div>
  if (!data.available || !config || !status) return null
  const currentStatus = status

  async function saveConfig(next: ClashConfig) { await run('config', () => webApi.saveClashConfig(next), '代理配置已保存'); setConfigOpen(false) }
  async function saveSubscription(name: string, url: string) { const current = editing; await run('subscription:save', () => current ? webApi.updateClashSubscription(current.id, name, url) : webApi.addClashSubscription(name, url), current ? '订阅已更新' : '订阅已添加'); setSubscriptionOpen(false); setEditing(null) }
  async function refreshSubscription(id: string) { setBusy(`subscription:${id}`); try { const result = await webApi.refreshClashSubscription(id); if (result.errors?.length) toast.warning(result.errors.join('; ')); else toast.success(`已刷新 ${result.totalNodes} 个节点`); await onRefresh() } catch (error) { const apiError = error as ApiError; if (apiError.status === 401 || apiError.status === 403) onAuthExpired(); else toast.error(error instanceof Error ? error.message : '刷新订阅失败') } finally { setBusy('') } }
  async function copyAddress() { if (!currentStatus.socksAddr) return; try { await copyToClipboard(`socks5://${currentStatus.socksAddr}`); toast.success('代理地址已复制') } catch { toast.error('复制代理地址失败') } }

  return <div className="proxy-page">
    <section className="card proxy-overview">
      <div className="card-header proxy-overview-header">
        <div className="proxy-status-block"><span className="proxy-globe"><Globe size={18} /></span><div><h2 className="card-title">代理</h2><div className="proxy-status-meta"><span className={`status-badge ${status.running ? 'running' : 'stopped'}`}>{status.running ? '运行中' : '已停止'}</span>{status.running ? <button type="button" className="proxy-copy-address" onClick={() => void copyAddress()}>SOCKS5://{status.socksAddr || '--'} <ClipboardCopy size={13} /></button> : null}<span>节点：{status.selectedNode || '--'}</span></div></div></div>
        <div className="actions"><button className={`btn ${status.running ? 'danger' : 'primary'}`} disabled={busy === 'runtime'} onClick={() => void run('runtime', status.running ? webApi.stopClash : webApi.startClash, status.running ? '代理已停止' : '代理已启动')}>{status.running ? <Square size={15} /> : <Play size={15} />} {status.running ? '停止' : '启动'}</button><button className="btn" onClick={() => setConfigOpen(true)}><Settings2 size={15} /> 配置</button><button className="btn" disabled={busy === 'reload'} onClick={() => void run('reload', webApi.reloadClash, '代理配置已刷新')}><RefreshCw size={15} /> {busy === 'reload' ? '刷新中...' : '刷新'}</button><button className="btn primary" onClick={() => { setEditing(null); setSubscriptionOpen(true) }}>订阅</button></div>
      </div>
    </section>

    <section className="card proxy-subscriptions-section">
      <div className="card-header"><div><h2 className="card-title">订阅源</h2><div className="card-subtitle">管理订阅、活跃入口、链式出口和节点</div></div><span className="meta-pill">共 {subscriptions.length} 个</span></div>
      <div className="proxy-subscriptions-body">{subscriptions.length === 0 ? <div className="empty-state">暂无订阅源</div> : <div className="proxy-subscription-grid">{subscriptions.map((subscription) => { const subBusy = busy === `subscription:${subscription.id}`; const nodeCount = (data.nodes || []).filter((node) => node.sourceId === subscription.id).length; return <article key={subscription.id} className={`proxy-subscription-card${subscription.active ? ' active' : ''}${subscription.enabled ? '' : ' disabled'}`}><div className="proxy-subscription-title"><div><strong title={subscription.name || subscription.id}>{subscription.name || subscription.id}</strong><span>{nodeCount} 个节点</span></div><span className={`status-badge ${subscription.active ? 'running' : 'stopped'}`}>{subscription.active ? '已激活' : '未激活'}</span></div><div className="proxy-selected-node" title={subscription.selectedNode}>选中节点：{subscription.selectedNode || '--'}</div><div className="actions proxy-subscription-actions">{!subscription.active ? <button className="btn small" disabled={subBusy} onClick={() => void run(`subscription:${subscription.id}`, () => webApi.setActiveClashSubscription(subscription.id), '活跃订阅已更新')}><Play size={14} /> 激活</button> : null}<button className={`btn small${chainID === subscription.id ? ' primary' : ''}`} disabled={subBusy} onClick={() => void run(`subscription:${subscription.id}`, () => webApi.setClashChain(chainID === subscription.id ? '' : subscription.id), '链式代理已更新')}>链式代理</button><button className="btn small" disabled={subBusy} onClick={() => setNodesSubscription(subscription)}><Route size={14} /> 管理节点</button><button className="btn small" disabled={subBusy} onClick={() => { setEditing(subscription); setSubscriptionOpen(true) }}><Pencil size={14} /> 编辑</button><button className="btn small" disabled={subBusy} onClick={() => void refreshSubscription(subscription.id)}><RefreshCw size={14} /> {subBusy ? '处理中...' : '刷新'}</button><button className="btn small" disabled={subBusy} onClick={() => void run(`subscription:${subscription.id}`, () => webApi.toggleClashSubscription(subscription.id), subscription.enabled ? '订阅已禁用' : '订阅已启用')}>{subscription.enabled ? <Wifi size={14} /> : <WifiOff size={14} />} {subscription.enabled ? '禁用' : '启用'}</button><button className="btn danger small" disabled={subBusy} onClick={() => { if (window.confirm(`确定删除订阅 ${subscription.name || subscription.id} 吗？`)) void run(`subscription:${subscription.id}`, () => webApi.removeClashSubscription(subscription.id), '订阅已删除') }}><Trash2 size={14} /> 删除</button></div></article> })}</div>}</div>
    </section>

    <ClashConfigDialog open={configOpen} config={config} saving={busy === 'config'} onClose={() => setConfigOpen(false)} onSave={saveConfig} />
    <ClashSubscriptionDialog open={subscriptionOpen} subscription={editing} saving={busy === 'subscription:save'} onClose={() => { setSubscriptionOpen(false); setEditing(null) }} onSave={saveSubscription} />
    <ClashNodesDialog open={Boolean(nodesSubscription)} subscription={nodesSubscription} data={data} onClose={() => setNodesSubscription(null)} onSaved={onRefresh} onRefreshSubscription={refreshSubscription} onAuthExpired={onAuthExpired} />
  </div>
}
