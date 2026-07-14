import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { webApi } from '@/api/web'
import type { ApiError } from '@/api/client'
import { Dialog, DialogBody, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'

interface XaiSSOImportDialogProps {
  open: boolean
  onClose: () => void
  onImported: () => void | Promise<void>
  onAuthExpired: () => void
}

interface LineMessage {
  line: number
  message: string
}

interface ImportSummary {
  created: number
  updated: number
  skipped: number
  failures: LineMessage[]
  warnings: LineMessage[]
}

export default function XaiSSOImportDialog({ open, onClose, onImported, onAuthExpired }: XaiSSOImportDialogProps) {
  const [text, setText] = useState('')
  const [importing, setImporting] = useState(false)
  const [progress, setProgress] = useState(0)
  const [total, setTotal] = useState(0)
  const [result, setResult] = useState<ImportSummary | null>(null)

  useEffect(() => {
    if (!open) {
      setText('')
      setProgress(0)
      setTotal(0)
      setResult(null)
    }
  }, [open])

  if (!open) return null

  async function handleImport(): Promise<void> {
    if (importing) return
    const seen = new Set<string>()
    const entries: Array<{ line: number; sso: string }> = []
    let skipped = 0
    text.split(/\r?\n/).forEach((raw, index) => {
      const sso = raw.trim()
      if (!sso) return
      if (seen.has(sso)) {
        skipped += 1
        return
      }
      seen.add(sso)
      entries.push({ line: index + 1, sso })
    })
    if (entries.length === 0) {
      toast.error('请至少输入一个 SSO Cookie')
      return
    }

    setImporting(true)
    setProgress(0)
    setTotal(entries.length)
    const summary: ImportSummary = { created: 0, updated: 0, skipped, failures: [], warnings: [] }
    let changed = false
    try {
      for (let index = 0; index < entries.length; index += 1) {
        const entry = entries[index]
        setProgress(index + 1)
        try {
          const imported = await webApi.importXaiSSOAccount(entry.sso)
          if (imported.action === 'created') summary.created += 1
          else summary.updated += 1
          changed = true
          if (imported.warning) summary.warnings.push({ line: entry.line, message: imported.warning })
        } catch (error) {
          const apiError = error as ApiError
          if (apiError.status === 401 || apiError.status === 403) {
            onAuthExpired()
            return
          }
          summary.failures.push({ line: entry.line, message: apiError.message || 'SSO 导入失败' })
        }
      }
      setResult(summary)
      if (changed) await onImported()
    } finally {
      setImporting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={(nextOpen) => { if (!nextOpen && !importing) onClose() }}>
      <DialogContent closeDisabled={importing}>
        <DialogHeader>
          <div>
            <DialogTitle>通过 SSO 导入 xAI 账号</DialogTitle>
            <DialogDescription>一行一个 SSO Cookie；系统会获取 OAuth 凭据，重复账号将更新原账号。</DialogDescription>
          </div>
        </DialogHeader>

        <DialogBody>
          <div className="field">
            <label className="field-label">SSO Cookie</label>
            <textarea
              className="textarea"
              rows={12}
              value={text}
              onChange={(event) => setText(event.target.value)}
              placeholder="每行粘贴一个 SSO Cookie"
              disabled={importing}
            />
          </div>

          {importing ? <div className="notice info mt-14">正在处理 {progress} / {total}</div> : null}
          {result ? (
            <div className="mt-14">
              <div className="sso-import-results">
                <span className="meta-pill">新增: {result.created}</span>
                <span className="meta-pill">更新: {result.updated}</span>
                <span className="meta-pill">失败: {result.failures.length}</span>
                <span className="meta-pill">警告: {result.warnings.length}</span>
                <span className="meta-pill">重复输入已忽略: {result.skipped}</span>
              </div>
              {result.failures.length > 0 ? (
                <div className="notice error mt-14">
                  <strong>失败详情</strong>
                  <ul className="sso-import-result-list">
                    {result.failures.map((item) => <li key={`failure-${item.line}`}>第 {item.line} 行：{item.message}</li>)}
                  </ul>
                </div>
              ) : null}
              {result.warnings.length > 0 ? (
                <div className="notice warning mt-14">
                  <strong>警告详情</strong>
                  <ul className="sso-import-result-list">
                    {result.warnings.map((item) => <li key={`warning-${item.line}`}>第 {item.line} 行：{item.message}</li>)}
                  </ul>
                </div>
              ) : null}
            </div>
          ) : null}
        </DialogBody>

        <DialogFooter>
          <button type="button" className="btn" onClick={onClose} disabled={importing}>关闭</button>
          <button type="button" className="btn primary" onClick={() => void handleImport()} disabled={importing}>
            {importing ? '导入中...' : '导入'}
          </button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
