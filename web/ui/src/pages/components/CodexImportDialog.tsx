import { useEffect, useId, useRef, useState, type ChangeEvent } from 'react'
import { toast } from 'sonner'
import { CloseIcon } from '@/components/icons'
import { webApi } from '@/api/web'
import type { ApiError } from '@/api/client'
import { buildCodexImportDTOs, parseCodexJsonFile } from '@/lib/codexImport'
import type { CodexAccountInput } from '@/types'

interface CodexImportDialogProps {
  open: boolean
  onClose: () => void
  onSuccess: () => void | Promise<void>
}

function toErrorMessage(error: unknown): string {
  const apiError = error as ApiError
  return apiError?.message || (error instanceof Error ? error.message : String(error))
}

export default function CodexImportDialog({ open, onClose, onSuccess }: CodexImportDialogProps) {
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

  async function mergeAccountsIntoEditor(accounts: CodexAccountInput[]): Promise<void> {
    const existing = jsonText.trim() ? buildCodexImportDTOs(jsonText).dtos : []
    const next = [...existing, ...accounts]
    setJsonText(JSON.stringify(next, null, 2))
    setFileCount(next.length)
  }

  async function handleFileChange(event: ChangeEvent<HTMLInputElement>): Promise<void> {
    const files = Array.from(event.target.files || [])
    if (!files.length) return

    const accounts: CodexAccountInput[] = []
    const errors: string[] = []

    for (const file of files) {
      try {
        const text = await file.text()
        const data: unknown = JSON.parse(text)
        const account = parseCodexJsonFile(data)
        if (account) {
          accounts.push(account)
        } else {
          errors.push(`${file.name}: 不是有效的 Codex 单账号 JSON`)
        }
      } catch (error) {
        errors.push(`${file.name}: ${toErrorMessage(error)}`)
      }
    }

    if (accounts.length > 0) {
      await mergeAccountsIntoEditor(accounts)
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
      toast.error('请先选择 JSON 文件或粘贴 JSON 内容')
      return
    }

    const { dtos, errors } = buildCodexImportDTOs(jsonText)
    if (!dtos.length) {
      toast.error(errors.length ? errors.slice(0, 3).join('\n') : '未找到可导入的账号')
      return
    }

    const confirmed = errors.length
      ? window.confirm(`导入 ${dtos.length} 个账号？（${errors.length} 个无效条目将跳过）`)
      : window.confirm(`导入 ${dtos.length} 个账号？`)
    if (!confirmed) return

    setImporting(true)
    try {
      let successCount = 0
      let failedCount = 0
      let skippedCount = 0

      for (const dto of dtos) {
        try {
          await webApi.addCodexAccount(dto)
          successCount += 1
        } catch (error) {
          const reason = toErrorMessage(error).toLowerCase()
          if (reason.includes('already exists') || reason.includes('duplicate')) {
            skippedCount += 1
          } else {
            failedCount += 1
          }
        }
      }

      let message = `JSON 导入完成：成功 ${successCount}，失败 ${failedCount}`
      if (skippedCount > 0) {
        message += `，跳过重复 ${skippedCount}`
      }
      toast.success(message)

      if (successCount > 0) {
        await onSuccess()
      }
      onClose()
    } catch (error) {
      toast.error(`JSON 导入失败：${toErrorMessage(error)}`)
    } finally {
      setImporting(false)
    }
  }

  return (
    <div className="dialog-backdrop" onClick={() => !importing && onClose()}>
      <div className="dialog-card" onClick={(event) => event.stopPropagation()}>
        <div className="card-header">
          <div>
            <h2 className="card-title">导入 Codex 账号 (JSON)</h2>
            <div className="card-subtitle">支持上传桌面版导出的单账号 JSON，或直接粘贴账号数组 JSON</div>
          </div>
          <button className="btn dialog-close-btn" type="button" aria-label="关闭" title="关闭" onClick={onClose} disabled={importing}>
            <CloseIcon />
          </button>
        </div>

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
            {fileCount > 0 ? <span className="muted small">已载入 {fileCount} 个账号到编辑区</span> : null}
          </div>
        </div>

        <div className="field mt-14">
          <label className="field-label">粘贴 JSON</label>
          <textarea
            className="textarea"
            rows={14}
            value={jsonText}
            onChange={(event) => setJsonText(event.target.value)}
            placeholder={'支持以下格式：\n1. 单个账号对象\n2. 账号数组\n3. { "accounts": [...] }'}
          />
        </div>

        <div className="actions mt-18 dialog-actions">
          <button type="button" className="btn" onClick={onClose} disabled={importing}>
            取消
          </button>
          <button type="button" className="btn primary" onClick={() => void handleImport()} disabled={importing}>
            {importing ? '导入中...' : '导入'}
          </button>
        </div>
      </div>
    </div>
  )
}
