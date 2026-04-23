import type { FormEvent } from 'react'
import { Dialog, DialogBody, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { DEFAULT_CODEX_CONFIG } from '@/lib/codex'
import type { CodexConfigForm } from '@/types'

interface CodexConfigDialogProps {
  open: boolean
  form: CodexConfigForm | null
  saving: boolean
  onClose: () => void
  onChange: (form: CodexConfigForm) => void
  onSubmit: (event: FormEvent<HTMLFormElement>) => void | Promise<void>
}

export default function CodexConfigDialog({ open, form, saving, onClose, onChange, onSubmit }: CodexConfigDialogProps) {
  if (!open || !form) return null

  const updateField = <K extends keyof CodexConfigForm>(key: K, value: CodexConfigForm[K]) => onChange({ ...form, [key]: value })

  return (
    <Dialog open={open} onOpenChange={(nextOpen) => {
      if (!nextOpen && !saving) onClose()
    }}>
      <DialogContent closeDisabled={saving}>
        <DialogHeader>
          <div>
            <DialogTitle>Codex 全局配置</DialogTitle>
            <DialogDescription>保存后立即写入 codex 多账号配置，并实时刷新账号池</DialogDescription>
          </div>
        </DialogHeader>

        <form onSubmit={onSubmit}>
          <DialogBody>
            <div className="field-row">
              <div className="field">
                <label className="field-label">Rotation Mode</label>
                <select className="select" value={form.rotationMode} onChange={(event) => updateField('rotationMode', event.target.value)}>
                  <option value="fixed">fixed</option>
                  <option value="failover">failover</option>
                  <option value="loadbalance">loadbalance</option>
                </select>
              </div>

              <div className="field">
                <label className="field-label">Proxy URL</label>
                <input
                  className="input"
                  value={form.proxyUrl}
                  onChange={(event) => updateField('proxyUrl', event.target.value)}
                  placeholder="socks5://127.0.0.1:1080"
                />
              </div>
            </div>

            <div className="field mt-14">
              <label className="field-label">Base URL</label>
              <input
                className="input"
                value={form.baseURL}
                onChange={(event) => updateField('baseURL', event.target.value)}
                placeholder={DEFAULT_CODEX_CONFIG.baseURL}
              />
            </div>

            <div className="field-row mt-14">
              <div className="field">
                <label className="field-label">Client Version</label>
                <input
                  className="input"
                  value={form.clientVersion}
                  onChange={(event) => updateField('clientVersion', event.target.value)}
                  placeholder={DEFAULT_CODEX_CONFIG.clientVersion}
                />
              </div>

              <div className="field">
                <label className="field-label">Originator</label>
                <input
                  className="input"
                  value={form.originator}
                  onChange={(event) => updateField('originator', event.target.value)}
                  placeholder={DEFAULT_CODEX_CONFIG.originator}
                />
              </div>
            </div>

            <div className="field mt-14">
              <label className="field-label">User Agent</label>
              <textarea
                className="textarea"
                rows={3}
                value={form.userAgent}
                onChange={(event) => updateField('userAgent', event.target.value)}
                placeholder={DEFAULT_CODEX_CONFIG.userAgent}
              />
            </div>
          </DialogBody>

          <DialogFooter>
            <button type="button" className="btn" onClick={onClose} disabled={saving}>
              取消
            </button>
            <button type="submit" className="btn primary" disabled={saving}>
              {saving ? '保存中...' : '保存配置'}
            </button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
