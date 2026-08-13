import { useEffect, useState, type FormEvent } from 'react'
import { Dialog, DialogBody, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import type { ClashConfig } from '@/types'

interface Props {
  open: boolean
  config: ClashConfig
  saving: boolean
  onClose: () => void
  onSave: (config: ClashConfig) => void | Promise<void>
}

export default function ClashConfigDialog({ open, config, saving, onClose, onSave }: Props) {
  const [form, setForm] = useState({ socksListen: '127.0.0.1', socksPort: 10808, logLevel: 'warning', userYaml: '' })

  useEffect(() => {
    if (!open) return
    setForm({
      socksListen: String(config.socksListen || '127.0.0.1'),
      socksPort: Number(config.socksPort || 10808),
      logLevel: String(config.logLevel || 'warning'),
      userYaml: String(config.userYaml || ''),
    })
  }, [open, config])

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    void onSave({
      ...config,
      socksListen: form.socksListen.trim() || '127.0.0.1',
      socksPort: Number(form.socksPort),
      logLevel: form.logLevel,
      userYaml: form.userYaml,
    })
  }

  return (
    <Dialog open={open} onOpenChange={(next) => { if (!next && !saving) onClose() }}>
      <DialogContent className="proxy-config-dialog" closeDisabled={saving}>
        <DialogHeader><div><DialogTitle>代理配置</DialogTitle><DialogDescription>配置 SOCKS5 监听地址、日志级别和附加 Mihomo YAML</DialogDescription></div></DialogHeader>
        <form onSubmit={submit}>
          <DialogBody>
            <div className="field-row">
              <div className="field"><label className="field-label">监听地址</label><input className="input" value={form.socksListen} onChange={(e) => setForm({ ...form, socksListen: e.target.value })} /></div>
              <div className="field"><label className="field-label">混合端口</label><input className="input" type="number" min={1} max={65535} value={form.socksPort} onChange={(e) => setForm({ ...form, socksPort: Number(e.target.value) })} /></div>
            </div>
            <div className="field mt-14"><label className="field-label">日志级别</label><select className="select" value={form.logLevel} onChange={(e) => setForm({ ...form, logLevel: e.target.value })}><option value="debug">Debug</option><option value="info">Info</option><option value="warning">Warning</option><option value="error">Error</option><option value="none">None</option></select></div>
            <div className="field mt-14"><label className="field-label">user.yaml</label><textarea className="textarea proxy-yaml" rows={15} spellCheck={false} value={form.userYaml} onChange={(e) => setForm({ ...form, userYaml: e.target.value })} placeholder={'global-client-fingerprint: chrome\nexternal-controller: :9090\n\ndns:\n  enable: true'} /><div className="muted">可追加启动 YAML 自定义项；mixed-port、bind-address 和 allow-lan 仍由上方配置控制。</div></div>
          </DialogBody>
          <DialogFooter><button type="button" className="btn" disabled={saving} onClick={onClose}>取消</button><button type="submit" className="btn primary" disabled={saving || form.socksPort < 1 || form.socksPort > 65535}>{saving ? '保存中...' : '保存'}</button></DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
