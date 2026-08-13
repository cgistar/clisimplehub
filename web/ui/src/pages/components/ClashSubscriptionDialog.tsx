import { useEffect, useState, type FormEvent } from 'react'
import { Dialog, DialogBody, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import type { ClashSubscription } from '@/types'

interface Props { open: boolean; subscription: ClashSubscription | null; saving: boolean; onClose: () => void; onSave: (name: string, url: string) => void | Promise<void> }

export default function ClashSubscriptionDialog({ open, subscription, saving, onClose, onSave }: Props) {
  const [name, setName] = useState('')
  const [url, setURL] = useState('')
  useEffect(() => { if (open) { setName(subscription?.name || ''); setURL(subscription?.url || '') } }, [open, subscription])
  function submit(event: FormEvent<HTMLFormElement>) { event.preventDefault(); void onSave(name.trim(), url.trim()) }
  return <Dialog open={open} onOpenChange={(next) => { if (!next && !saving) onClose() }}><DialogContent className="dialog-card-narrow" closeDisabled={saving}><DialogHeader><div><DialogTitle>{subscription ? '编辑订阅' : '添加订阅'}</DialogTitle><DialogDescription>新增订阅需要 URL；已有订阅可清空 URL 后仅维护手工节点</DialogDescription></div></DialogHeader><form onSubmit={submit}><DialogBody><div className="field"><label className="field-label">订阅名称</label><input className="input" value={name} onChange={(e) => setName(e.target.value)} placeholder="Unnamed" /></div><div className="field mt-14"><label className="field-label">订阅 URL</label><input className="input" value={url} onChange={(e) => setURL(e.target.value)} placeholder="https://example.com/subscription" /></div></DialogBody><DialogFooter><button type="button" className="btn" onClick={onClose} disabled={saving}>取消</button><button type="submit" className="btn primary" disabled={saving || (!subscription && !url.trim())}>{saving ? '保存中...' : '保存'}</button></DialogFooter></form></DialogContent></Dialog>
}
