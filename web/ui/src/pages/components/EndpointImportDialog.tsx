import { useEffect, useId, useRef, useState, type ChangeEvent } from 'react'
import { toast } from 'sonner'
import { webApi } from '@/api/web'
import type { ApiError } from '@/api/client'
import { buildEndpointImportDTOs } from '@/lib/endpointImport'
import { Dialog, DialogBody, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'

interface EndpointImportDialogProps {
  open: boolean
  onClose: () => void
  onSuccess: () => void | Promise<void>
  onAuthExpired: () => void
}

function toErrorMessage(error: unknown): string {
  const apiError = error as ApiError
  return apiError?.message || (error instanceof Error ? error.message : String(error))
}

function isAuthError(error: unknown): boolean {
  const status = (error as ApiError)?.status
  return status === 401 || status === 403
}

export default function EndpointImportDialog({ open, onClose, onSuccess, onAuthExpired }: EndpointImportDialogProps) {
  const fileInputId = useId()
  const fileInputRef = useRef<HTMLInputElement | null>(null)
  const [jsonText, setJsonText] = useState<string>('')
  const [fileCount, setFileCount] = useState<number>(0)
  const [importing, setImporting] = useState<boolean>(false)

  useEffect(() => {
    if (!open) {
      setJsonText('')
      setFileCount(0)
    }
  }, [open])

  if (!open) return null

  async function handleFileChange(event: ChangeEvent<HTMLInputElement>): Promise<void> {
    const files = Array.from(event.target.files || [])
    if (!files.length) return

    const payloads: unknown[] = []
    const errors: string[] = []
    for (const file of files) {
      try {
        const text = await file.text()
        const parsed = JSON.parse(text) as unknown
        if (Array.isArray(parsed)) {
          payloads.push(...parsed)
        } else if (parsed && typeof parsed === 'object' && Array.isArray((parsed as { endpoints?: unknown }).endpoints)) {
          payloads.push(...((parsed as { endpoints: unknown[] }).endpoints))
        } else {
          errors.push(`${file.name}: 请提供 endpoint JSON 数组`)
        }
      } catch (error) {
        errors.push(`${file.name}: ${toErrorMessage(error)}`)
      }
    }

    if (payloads.length > 0) {
      setJsonText(JSON.stringify(payloads, null, 2))
      setFileCount(payloads.length)
    }
    if (errors.length > 0) {
      toast.error(errors.slice(0, 3).join('\n'))
    }
    if (fileInputRef.current) {
      fileInputRef.current.value = ''
    }
  }

  async function handleImport(): Promise<void> {
    if (!jsonText.trim()) {
      toast.error('请先选择 JSON 文件或粘贴 endpoint JSON 数组')
      return
    }

    const { dtos, errors } = buildEndpointImportDTOs(jsonText)
    if (!dtos.length) {
      toast.error(errors.length ? errors.slice(0, 3).join('\n') : '未找到可导入的端点')
      return
    }

    const confirmed = errors.length
      ? window.confirm(`导入 ${dtos.length} 个端点？（${errors.length} 个无效条目将跳过）`)
      : window.confirm(`导入 ${dtos.length} 个端点？`)
    if (!confirmed) return

    setImporting(true)
    try {
      const result = await webApi.importEndpoints(dtos)
      const success = Number(result.success) || 0
      const failed = result.failed?.length || 0
      toast.success(result.message || `端点导入完成：成功 ${success}，失败 ${failed}`)
      if (failed > 0) {
        toast.error(result.failed?.slice(0, 3).map((item) => `#${item.index + 1}: ${item.error}`).join('\n') || '部分端点导入失败')
      }
      if (success > 0) {
        await onSuccess()
      }
      onClose()
    } catch (error) {
      if (isAuthError(error)) {
        onAuthExpired()
        return
      }
      toast.error(`端点导入失败：${toErrorMessage(error)}`)
    } finally {
      setImporting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={(nextOpen) => {
      if (!nextOpen && !importing) onClose()
    }}>
      <DialogContent closeDisabled={importing}>
        <DialogHeader>
          <div>
            <DialogTitle>导入端点 (JSON)</DialogTitle>
            <DialogDescription>支持 endpoint JSON 数组，必填字段：name、apiUrl、apiKey、interfaceType</DialogDescription>
          </div>
        </DialogHeader>

        <DialogBody>
          <div className="field">
            <label className="field-label" htmlFor={fileInputId}>选择 JSON 文件</label>
            <div className="actions">
              <input
                id={fileInputId}
                ref={fileInputRef}
                type="file"
                accept=".json,application/json"
                multiple
                hidden
                onChange={(event) => void handleFileChange(event)}
              />
              <button className="btn" type="button" onClick={() => fileInputRef.current?.click()} disabled={importing}>
                选择文件
              </button>
              {fileCount > 0 ? <span className="muted small">已载入 {fileCount} 个端点到编辑区</span> : null}
            </div>
          </div>

          <div className="field mt-14">
            <label className="field-label">粘贴 JSON</label>
            <textarea
              className="textarea"
              rows={14}
              value={jsonText}
              onChange={(event) => setJsonText(event.target.value)}
              placeholder={'[\n  {\n    "name": "my-endpoint",\n    "apiUrl": "https://example.com",\n    "apiKey": "sk-...",\n    "interfaceType": "claude",\n    "providerName": "provider",\n    "enabled": true,\n    "priority": 5\n  }\n]'}
            />
          </div>
        </DialogBody>

        <DialogFooter>
          <button type="button" className="btn" onClick={onClose} disabled={importing}>
            取消
          </button>
          <button type="button" className="btn primary" onClick={() => void handleImport()} disabled={importing}>
            {importing ? '导入中...' : '导入'}
          </button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
