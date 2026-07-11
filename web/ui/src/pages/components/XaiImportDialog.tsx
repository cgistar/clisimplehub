import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import { webApi } from '@/api/web'
import type { ApiError } from '@/api/client'
import { Dialog, DialogBody, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { parseXaiImportAccounts } from '@/lib/xai'

interface XaiImportDialogProps {
  open: boolean
  onClose: () => void
  onImported: () => void | Promise<void>
}

export default function XaiImportDialog({ open, onClose, onImported }: XaiImportDialogProps) {
  const [text, setText] = useState('')
  const [importing, setImporting] = useState(false)

  useEffect(() => {
    if (!open) setText('')
  }, [open])

  if (!open) return null

  async function handleImport() {
    if (!text.trim()) {
      toast.error('请粘贴 JSON 内容')
      return
    }
    let parsed: unknown
    try {
      parsed = JSON.parse(text)
    } catch {
      toast.error('JSON 解析失败')
      return
    }
    const accounts = parseXaiImportAccounts(parsed)
    if (accounts.length === 0) {
      toast.error('未找到有效账号')
      return
    }
    if (!window.confirm(`导入 ${accounts.length} 个账号？`)) return

    setImporting(true)
    let success = 0
    let failed = 0
    try {
      for (const account of accounts) {
        try {
          await webApi.addXaiAccount(account)
          success += 1
        } catch {
          failed += 1
        }
      }
      toast.success(`导入完成：${success} 成功，${failed} 失败`)
      setText('')
      onClose()
      await onImported()
    } catch (error) {
      toast.error((error as ApiError).message || '导入失败')
    } finally {
      setImporting(false)
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(nextOpen) => {
        if (!nextOpen && !importing) onClose()
      }}
    >
      <DialogContent closeDisabled={importing}>
        <DialogHeader>
          <div>
            <DialogTitle>导入 xAI 账号</DialogTitle>
            <DialogDescription>支持内部账号 JSON、Grok CLI auth.json，以及 {"{"}"sso":"..."{"}"}</DialogDescription>
          </div>
        </DialogHeader>

        <DialogBody>
          <div className="field">
            <label className="field-label">JSON 内容</label>
            <textarea
              className="textarea"
              rows={14}
              value={text}
              onChange={(event) => setText(event.target.value)}
              placeholder="粘贴 JSON..."
              disabled={importing}
            />
          </div>
        </DialogBody>

        <DialogFooter>
          <button type="button" className="btn" onClick={onClose} disabled={importing}>
            取消
          </button>
          <button type="button" className="btn primary" disabled={importing} onClick={() => void handleImport()}>
            {importing ? '导入中...' : '导入'}
          </button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
