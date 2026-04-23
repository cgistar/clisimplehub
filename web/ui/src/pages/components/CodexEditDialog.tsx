import type { FormEvent } from 'react'
import { CloseIcon } from '@/components/icons'
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
    <div className="dialog-backdrop" onClick={() => !saving && onClose()}>
      <div className="dialog-card dialog-card-narrow" onClick={(event) => event.stopPropagation()}>
        <div className="card-header">
          <div>
            <h2 className="card-title">编辑 Codex 账号</h2>
            <div className="card-subtitle">与桌面版一致：仅编辑本地账号附加字段，不改账号主身份</div>
          </div>
          <button className="btn dialog-close-btn" type="button" aria-label="关闭" title="关闭" onClick={onClose} disabled={saving}>
            <CloseIcon />
          </button>
        </div>

        <form onSubmit={onSubmit}>
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

          <div className="actions mt-18 dialog-actions">
            <button type="button" className="btn" onClick={onClose} disabled={saving}>
              取消
            </button>
            <button type="submit" className="btn primary" disabled={saving}>
              {saving ? '保存中...' : '保存'}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}
