import type { FormEvent } from 'react'
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
    <div className="dialog-backdrop" onClick={() => !saving && onClose()}>
      <div className="dialog-card" onClick={(event) => event.stopPropagation()}>
        <div className="card-header">
          <div>
            <h2 className="card-title">Codex 全局配置</h2>
            <div className="card-subtitle">保存后立即写入 codex 多账号配置，并实时刷新账号池</div>
          </div>
          <button className="btn" type="button" onClick={onClose} disabled={saving}>
            关闭
          </button>
        </div>

        <form onSubmit={onSubmit}>
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

          <div className="actions mt-18 dialog-actions">
            <button type="button" className="btn" onClick={onClose} disabled={saving}>
              取消
            </button>
            <button type="submit" className="btn primary" disabled={saving}>
              {saving ? '保存中...' : '保存配置'}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}
