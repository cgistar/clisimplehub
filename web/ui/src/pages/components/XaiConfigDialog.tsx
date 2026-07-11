import type { FormEvent } from 'react'
import { Dialog, DialogBody, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { DEFAULT_XAI_CONFIG } from '@/lib/xai'
import type { XaiConfigForm } from '@/types'

interface XaiConfigDialogProps {
  open: boolean
  form: XaiConfigForm | null
  saving: boolean
  onClose: () => void
  onChange: (form: XaiConfigForm) => void
  onSubmit: (event: FormEvent<HTMLFormElement>) => void | Promise<void>
}

export default function XaiConfigDialog({ open, form, saving, onClose, onChange, onSubmit }: XaiConfigDialogProps) {
  if (!open || !form) return null

  const updateField = <K extends keyof XaiConfigForm>(key: K, value: XaiConfigForm[K]) => onChange({ ...form, [key]: value })

  return (
    <Dialog
      open={open}
      onOpenChange={(nextOpen) => {
        if (!nextOpen && !saving) onClose()
      }}
    >
      <DialogContent closeDisabled={saving}>
        <DialogHeader>
          <div>
            <DialogTitle>xAI 全局配置</DialogTitle>
            <DialogDescription>保存后立即写入 xai.json，并实时刷新账号池</DialogDescription>
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
                placeholder={DEFAULT_XAI_CONFIG.baseURL}
              />
            </div>

            <div className="field-row mt-14">
              <div className="field">
                <label className="field-label">Client Version</label>
                <input
                  className="input"
                  value={form.clientVersion}
                  onChange={(event) => updateField('clientVersion', event.target.value)}
                  placeholder="0.2.93"
                />
              </div>
              <div className="field">
                <label className="field-label">Client Surface</label>
                <input
                  className="input"
                  value={form.clientSurface}
                  onChange={(event) => updateField('clientSurface', event.target.value)}
                  placeholder="grok-cli"
                />
              </div>
            </div>

            <div className="field mt-14">
              <label className="field-label">User Agent</label>
              <input
                className="input"
                value={form.userAgent}
                onChange={(event) => updateField('userAgent', event.target.value)}
                placeholder="xai-grok-cli/0.2.93"
              />
            </div>

            <div className="field mt-14">
              <label className="field-label">Token Auth</label>
              <input
                className="input"
                value={form.tokenAuth}
                onChange={(event) => updateField('tokenAuth', event.target.value)}
                placeholder="xai-grok-cli"
              />
            </div>

            <div className="field mt-14">
              <label className="field-label">Dynamic Statsig</label>
              <label className="checkbox-row">
                <input
                  type="checkbox"
                  checked={form.dynamicStatsig !== false}
                  onChange={(event) => updateField('dynamicStatsig', event.target.checked)}
                />
                <span>动态生成 x-statsig-id（grok.com rate-limits，默认开）</span>
              </label>
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
