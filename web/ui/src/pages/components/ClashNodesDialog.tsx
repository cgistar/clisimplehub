import { useEffect, useMemo, useRef, useState } from 'react'
import { Check, ClipboardCopy, Plus, RefreshCw, Save, Trash2, Zap } from 'lucide-react'
import { toast } from 'sonner'
import { webApi } from '@/api/web'
import type { ApiError } from '@/api/client'
import { copyToClipboard } from '@/lib/format'
import type { ClashDraftNode, ClashNode, ClashPageData, ClashSubscription, ClashSpeedTestResult } from '@/types'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import ClashAddNodesDialog from './ClashAddNodesDialog'

const TEST_CONCURRENCY = 4

function cleanNode(node: ClashDraftNode): ClashNode { const { _draftAdded: _, ...clean } = node; return clean }
function stableNode(node: ClashDraftNode) { const { latency: _, ...stable } = cleanNode(node); return stable }
function uniqueName(name: string, nodes: Array<{ name: string }>): string { if (!nodes.some((item) => item.name === name)) return name; for (let index = 2; ; index += 1) { const candidate = `${name}_${index}`; if (!nodes.some((item) => item.name === candidate)) return candidate } }
function latencyText(node: ClashDraftNode): string { if (!node.latency) return '--'; if (node.latency < 0) return '失败'; return `${node.latency}ms` }
function latencyClass(node: ClashDraftNode): string { if (node.latency < 0 || node.latency >= 500) return 'bad'; if (node.latency >= 200) return 'warn'; if (node.latency > 0) return 'good'; return '' }
function canceled(result?: ClashSpeedTestResult): boolean { return String(result?.error || '').toLowerCase().includes('cancel') }

interface Props { open: boolean; subscription: ClashSubscription | null; data: ClashPageData; onClose: () => void; onSaved: () => void | Promise<void>; onRefreshSubscription: (id: string) => Promise<void>; onAuthExpired: () => void }

export default function ClashNodesDialog({ open, subscription, data, onClose, onSaved, onRefreshSubscription, onAuthExpired }: Props) {
  const [nodes, setNodes] = useState<ClashDraftNode[]>([])
  const [initialNodes, setInitialNodes] = useState<ClashDraftNode[]>([])
  const [selected, setSelected] = useState('')
  const [initialSelected, setInitialSelected] = useState('')
  const [addOpen, setAddOpen] = useState(false)
  const [adding, setAdding] = useState(false)
  const [saving, setSaving] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const [testingHTTP, setTestingHTTP] = useState<Record<string, boolean>>({})
  const [testingTCP, setTestingTCP] = useState<Record<string, boolean>>({})
  const [testingAllHTTP, setTestingAllHTTP] = useState(false)
  const [testingAllTCP, setTestingAllTCP] = useState(false)
  const session = useRef(0)

  useEffect(() => {
    if (!open || !subscription) return
    const source = (data.nodes || []).filter((node) => node.sourceId === subscription.id).map((node) => ({ ...node }))
    const nextSelected = subscription.selectedNode || source[0]?.name || ''
    setNodes(source); setInitialNodes(source.map((node) => ({ ...node }))); setSelected(nextSelected); setInitialSelected(nextSelected)
  }, [open, subscription, data.nodes])

  const dirty = useMemo(() => selected !== initialSelected || JSON.stringify(nodes.map(stableNode)) !== JSON.stringify(initialNodes.map(stableNode)), [nodes, initialNodes, selected, initialSelected])
  if (!subscription) return null
  const currentSubscription = subscription

  async function close() {
    if (dirty && !window.confirm('当前有未保存的节点变更，确定丢弃吗？')) return
    session.current += 1; setTestingHTTP({}); setTestingTCP({}); setTestingAllHTTP(false); setTestingAllTCP(false)
    try { await webApi.cancelClashTests() } catch { /* 本地状态已取消 */ }
    setAddOpen(false); onClose()
  }

  async function add(content: string) {
    setAdding(true)
    try {
      const parsed = await webApi.parseClashNodes(currentSubscription.id, content)
      const names = nodes.map((node) => ({ name: node.name })); const added: ClashDraftNode[] = []
      for (const raw of parsed) { const name = uniqueName(String(raw.name || 'node').trim() || 'node', names); names.push({ name }); added.push({ ...raw, name, sourceId: currentSubscription.id, latency: Number(raw.latency || 0), _draftAdded: true }) }
      setNodes((current) => [...current, ...added]); if (!selected && added.length) setSelected(added[0].name); setAddOpen(false); toast.success(`已添加 ${added.length} 个节点到草稿`)
    } catch (error) { const apiError = error as ApiError; if (apiError.status === 401 || apiError.status === 403) onAuthExpired(); else toast.error(error instanceof Error ? error.message : '解析节点失败') } finally { setAdding(false) }
  }

  function remove(name: string) { if (!window.confirm(`确定删除节点 ${name} 吗？`)) return; const next = nodes.filter((node) => node.name !== name); setNodes(next); if (selected === name) setSelected(next[0]?.name || '') }
  function updateLatency(name: string, result: ClashSpeedTestResult) { setNodes((current) => current.map((node) => node.name === name ? { ...node, latency: typeof result.latency === 'number' ? result.latency : -1 } : node)) }

  async function testOne(name: string, mode: 'http' | 'tcp') {
    const target = nodes.find((node) => node.name === name); if (!target || target._draftAdded) { toast.warning('请先保存新增节点再测速'); return }
    const currentSession = session.current; const setter = mode === 'http' ? setTestingHTTP : setTestingTCP
    setter((current) => ({ ...current, [name]: true }))
    try { const result = await webApi.testClashNode(name, mode); if (currentSession === session.current && !canceled(result)) updateLatency(name, result) }
    catch (error) { if (currentSession === session.current) { updateLatency(name, { nodeName: name, latency: -1 }); const apiError = error as ApiError; if (apiError.status === 401 || apiError.status === 403) onAuthExpired(); else toast.error(error instanceof Error ? error.message : '测速失败') } }
    finally { setter((current) => { const next = { ...current }; delete next[name]; return next }) }
  }

  async function testAll(mode: 'http' | 'tcp') {
    const queue = nodes.filter((node) => !node._draftAdded).map((node) => node.name); if (!queue.length) return
    const currentSession = session.current; const setAll = mode === 'http' ? setTestingAllHTTP : setTestingAllTCP
    setAll(true); setNodes((current) => current.map((node) => node._draftAdded ? node : { ...node, latency: 0 }))
    const worker = async () => { while (queue.length && currentSession === session.current) { const name = queue.shift(); if (name) await testOne(name, mode) } }
    try { await Promise.all(Array.from({ length: Math.min(TEST_CONCURRENCY, queue.length) }, worker)) } finally { setAll(false) }
  }

  async function copyNode(node: ClashDraftNode) { try { const text = node._draftAdded ? JSON.stringify(cleanNode(node), null, 2) : (await webApi.getClashNodeConfig(node.name)).config; await copyToClipboard(text); toast.success('节点配置已复制') } catch (error) { toast.error(error instanceof Error ? error.message : '复制失败') } }

  async function save() {
    if (nodes.length && !selected) { toast.error('请选择一个节点'); return }
    const clean = nodes.map((node) => ({ ...cleanNode(node), sourceId: currentSubscription.id })); const finalSelected = clean.some((node) => node.name === selected) ? selected : clean[0]?.name || ''
    setSaving(true)
    try { session.current += 1; await webApi.cancelClashTests(); await webApi.replaceClashNodes(currentSubscription.id, clean, finalSelected); toast.success('节点已保存'); onClose(); await onSaved() }
    catch (error) { const apiError = error as ApiError; if (apiError.status === 401 || apiError.status === 403) onAuthExpired(); else toast.error(error instanceof Error ? error.message : '保存节点失败') } finally { setSaving(false) }
  }

  async function refresh() { if (dirty && !window.confirm('刷新订阅会丢弃未保存的节点变更，确定继续吗？')) return; setRefreshing(true); try { await onRefreshSubscription(currentSubscription.id) } finally { setRefreshing(false) } }

  return <>
    <Dialog open={open} onOpenChange={(next) => { if (!next) void close() }}>
      <DialogContent className="proxy-nodes-dialog" showCloseButton={false}>
        <DialogHeader><div><DialogTitle>管理节点 · {subscription.name || subscription.id}</DialogTitle><DialogDescription>{dirty ? '有未保存的节点变更' : '选择节点、批量导入、复制配置或测试延迟'}</DialogDescription></div><button className="btn small" type="button" onClick={() => void close()}>关闭</button></DialogHeader>
        <div className="proxy-dialog-toolbar"><button className="btn primary small" onClick={() => setAddOpen(true)}><Plus size={15} /> 添加节点</button><button className="btn small" disabled={refreshing} onClick={() => void refresh()}><RefreshCw size={15} /> {refreshing ? '刷新中...' : '刷新订阅'}</button><button className="btn small" disabled={testingAllHTTP} onClick={() => void testAll('http')}><Zap size={15} /> {testingAllHTTP ? '测速中...' : '全部测速'}</button><button className="btn small" disabled={testingAllTCP} onClick={() => void testAll('tcp')}><Zap size={15} /> {testingAllTCP ? '测速中...' : '全部 TCP 测速'}</button></div>
        <div className="proxy-nodes-body">{nodes.length === 0 ? <div className="empty-state">暂无节点，请添加节点或刷新订阅</div> : <div className="proxy-node-grid">{nodes.map((node) => <article key={node.name} className={`proxy-node-card${node.name === selected ? ' selected' : ''}`} onClick={() => setSelected(node.name)}><div className="proxy-node-title"><div><strong title={node.name}>{node.name}</strong><span>{node.type}{node._draftAdded ? ' · 未保存' : ''}</span></div>{node.name === selected ? <span className="status-badge running"><Check size={13} /> 已选</span> : null}</div><div className="proxy-node-address" title={`${node.server}:${node.port}`}>{node.server}:{node.port}</div><div className="proxy-node-footer"><span className={`proxy-latency ${latencyClass(node)}`}>{latencyText(node)}</span><div className="actions"><button className="btn small icon-only" title="复制配置" onClick={(e) => { e.stopPropagation(); void copyNode(node) }}><ClipboardCopy size={14} /></button><button className="btn danger small icon-only" title="删除" onClick={(e) => { e.stopPropagation(); remove(node.name) }}><Trash2 size={14} /></button><button className="btn small icon-only" disabled={node._draftAdded || testingHTTP[node.name]} title={node._draftAdded ? '请先保存' : 'HTTP 测速'} onClick={(e) => { e.stopPropagation(); void testOne(node.name, 'http') }}><Zap size={14} /></button><button className="btn small" disabled={node._draftAdded || testingTCP[node.name]} title={node._draftAdded ? '请先保存' : 'TCP 测速'} onClick={(e) => { e.stopPropagation(); void testOne(node.name, 'tcp') }}>TCP</button></div></div></article>)}</div>}</div>
        <DialogFooter><button className="btn" onClick={() => void close()}>取消</button><button className="btn primary" disabled={saving} onClick={() => void save()}><Save size={15} /> {saving ? '保存中...' : '保存'}</button></DialogFooter>
      </DialogContent>
    </Dialog>
    <ClashAddNodesDialog open={addOpen} saving={adding} onClose={() => setAddOpen(false)} onAdd={add} />
  </>
}
