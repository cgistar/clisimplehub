import { useEffect, useState, type FormEvent } from 'react'
import { Dialog, DialogBody, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'

interface Props { open: boolean; saving: boolean; onClose: () => void; onAdd: (content: string) => void | Promise<void> }

export default function ClashAddNodesDialog({ open, saving, onClose, onAdd }: Props) {
  const [content, setContent] = useState('')
  useEffect(() => { if (open) setContent('') }, [open])
  function submit(event: FormEvent<HTMLFormElement>) { event.preventDefault(); void onAdd(content.trim()) }
  return <Dialog open={open} onOpenChange={(next) => { if (!next && !saving) onClose() }}><DialogContent closeDisabled={saving}><DialogHeader><div><DialogTitle>添加节点</DialogTitle><DialogDescription>支持 vless、vmess、trojan、ss 等 URI，以及 Clash YAML 或 JSON</DialogDescription></div></DialogHeader><form onSubmit={submit}><DialogBody><textarea className="textarea proxy-node-input" rows={14} value={content} onChange={(e) => setContent(e.target.value)} placeholder="在此粘贴节点内容，一行一个 URI，或粘贴 YAML/JSON" /></DialogBody><DialogFooter><button type="button" className="btn" onClick={onClose} disabled={saving}>取消</button><button type="submit" className="btn primary" disabled={saving || !content.trim()}>{saving ? '解析中...' : '添加'}</button></DialogFooter></form></DialogContent></Dialog>
}
