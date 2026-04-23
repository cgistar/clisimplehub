import type { FormEvent } from 'react'
import { Dialog, DialogBody, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import type { CodexEditForm } from '@/types'

interface CodexEditDialogProps {
  open: boolean
  form: CodexEditForm | null
  saving: boolean
  onClose: () => void
  onChange: (form: CodexEditForm) => void
  onSubmit: (event: FormEvent<HTMLFormElement>) => void | Promise<void>
}

export default function CodexEditDialog({ open, form, saving, onClose, onChange, onSubmit }: CodexEditDialogProps) {
  if (!open || !form) return null

  const updateField = <K extends keyof CodexEditForm>(key: K, value: CodexEditForm[K]) => onChange({ ...form, [key]: value })

  return (
    <Dialog open={open} onOpenChange={(nextOpen) => {
      if (!nextOpen && !saving) onClose()
    }}>
      <DialogContent className="dialog-card-narrow" closeDisabled={saving}>
        <DialogHeader>
          <div>
            <DialogTitle>编辑 Codex 账号</DialogTitle>
            <DialogDescription>与桌面版一致：仅编辑本地账号附加字段，不改账号主身份</DialogDescription>
          </div>
        </DialogHeader>

        <form onSubmit={onSubmit}>
          <DialogBody>
            <div className="field">
              <label className="field-label">Refresh Token</label>
              <textarea className="textarea" rows={3} value={form.refreshToken} readOnly disabled />
            </div>

            <div className="field mt-14">
              <label className="field-label">密码</label>
              <input className="input" type="password" value={form.password} onChange={(event) => updateField('password', event.target.value)} placeholder="可选：账号密码" />
            </div>

            <div className="field mt-14">
              <label className="field-label">MFA 验证码</label>
              <input className="input" value={form.mfaCode} onChange={(event) => updateField('mfaCode', event.target.value)} placeholder="可选：一次性验证码" />
            </div>

            <div className="field mt-14">
              <label className="field-label">代理 URL</label>
              <input className="input" value={form.proxyUrl} onChange={(event) => updateField('proxyUrl', event.target.value)} placeholder="例如：socks5://127.0.0.1:1080" />
            </div>

            <div className="field mt-14">
              <label className="field-label">权重</label>
              <input
                className="input"
                type="number"
                min="0"
                max="100"
                value={form.weight}
                onChange={(event) => updateField('weight', Number(event.target.value || 0))}
              />
            </div>
          </DialogBody>

          <DialogFooter>
            <button type="button" className="btn" onClick={onClose} disabled={saving}>
              取消
            </button>
            <button type="submit" className="btn primary" disabled={saving}>
              {saving ? '保存中...' : '保存'}
            </button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
