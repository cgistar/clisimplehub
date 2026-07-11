import type { FormEvent } from 'react'
import { Dialog, DialogBody, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import type { XaiEditForm } from '@/types'

interface XaiEditDialogProps {
  open: boolean
  form: XaiEditForm | null
  saving: boolean
  onClose: () => void
  onChange: (form: XaiEditForm) => void
  onSubmit: (event: FormEvent<HTMLFormElement>) => void | Promise<void>
}

export default function XaiEditDialog({ open, form, saving, onClose, onChange, onSubmit }: XaiEditDialogProps) {
  if (!open || !form) return null

  const updateField = <K extends keyof XaiEditForm>(key: K, value: XaiEditForm[K]) => onChange({ ...form, [key]: value })

  return (
    <Dialog
      open={open}
      onOpenChange={(nextOpen) => {
        if (!nextOpen && !saving) onClose()
      }}
    >
      <DialogContent className="dialog-card-narrow" closeDisabled={saving}>
        <DialogHeader>
          <div>
            <DialogTitle>编辑 xAI 账号</DialogTitle>
            <DialogDescription>编辑账号 token、代理与能力开关</DialogDescription>
          </div>
        </DialogHeader>

        <form onSubmit={onSubmit}>
          <DialogBody>
            <div className="field">
              <label className="field-label">Email</label>
              <input className="input" value={form.email} onChange={(event) => updateField('email', event.target.value)} placeholder="可选显示名" />
            </div>

            <div className="field mt-14">
              <label className="field-label">Refresh Token</label>
              <textarea
                className="textarea"
                rows={3}
                value={form.refreshToken}
                onChange={(event) => updateField('refreshToken', event.target.value)}
              />
            </div>

            <div className="field mt-14">
              <label className="field-label">API Key</label>
              <input className="input" value={form.apiKey} onChange={(event) => updateField('apiKey', event.target.value)} placeholder="可选，API Key 账号填写" />
            </div>

            <div className="field mt-14">
              <label className="field-label">SSO Cookie</label>
              <textarea
                className="textarea"
                rows={2}
                value={form.sso}
                onChange={(event) => updateField('sso', event.target.value)}
                placeholder="粘贴 grok.com / accounts.x.ai 的 sso Cookie 值（JWT）"
              />
              <div className="field-help mt-8">用于 /xai/console 与额度刷新；有 SSO 时卡片会显示 SSO 标签</div>
            </div>

            <div className="field mt-14">
              <label className="field-label">代理 URL</label>
              <input
                className="input"
                value={form.proxyUrl}
                onChange={(event) => updateField('proxyUrl', event.target.value)}
                placeholder="例如：socks5://127.0.0.1:1080"
              />
            </div>

            <div className="field mt-14">
              <label className="field-label">能力开关</label>
              <label className="checkbox-row">
                <input type="checkbox" checked={form.enabled} onChange={(event) => updateField('enabled', event.target.checked)} />
                启用账号
              </label>
              <label className="checkbox-row mt-8">
                <input type="checkbox" checked={form.websockets} onChange={(event) => updateField('websockets', event.target.checked)} />
                Responses WebSockets
              </label>
              <label className="checkbox-row mt-8">
                <input type="checkbox" checked={form.usingApi} onChange={(event) => updateField('usingApi', event.target.checked)} />
                官方 API (using_api)
              </label>
              <div className="field-help mt-8">
                开启：文本走 api.x.ai；关闭：走 cli-chat-proxy（OAuth 账号默认关，适合 Build 额度）
              </div>
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
