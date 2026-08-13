import { useEffect, useState } from 'react'
import { Dialog, DialogBody, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import type { CodexResetCredit } from '@/types'
import { webApi } from '@/api/web'

interface CodexResetCreditsDialogProps {
  open: boolean
  accountId: string
  displayName: string
  busy: boolean
  onOpenChange: (open: boolean) => void
  onConfirm: (creditId: string) => void | Promise<void>
}

export default function CodexResetCreditsDialog({
  open,
  accountId,
  displayName,
  busy,
  onOpenChange,
  onConfirm,
}: CodexResetCreditsDialogProps) {
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string>('')
  const [actionError, setActionError] = useState<string>('')
  const [credits, setCredits] = useState<CodexResetCredit[]>([])
  const [submittingId, setSubmittingId] = useState<string>('')

  useEffect(() => {
    if (!open || !accountId) return
    let cancelled = false
    setLoading(true)
    setError('')
    setActionError('')
    setCredits([])
    setSubmittingId('')
    webApi
      .listCodexResetCredits(accountId)
      .then((list) => {
        if (cancelled) return
        setCredits(list?.credits ?? [])
      })
      .catch((cause: unknown) => {
        if (cancelled) return
        const detail = cause instanceof Error ? cause.message : String(cause)
        setError(detail ? `加载重置次数失败: ${detail}` : '加载重置次数失败')
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [open, accountId])

  function isUsable(item: CodexResetCredit): boolean {
    return Boolean(item.id?.trim()) && Boolean(item.is_supported_by_plan) && item.status === 'available'
  }

  function statusLabel(item: CodexResetCredit): string {
    if (item.status === 'available') return '可用'
    return item.status || '-'
  }

  async function handleReset(item: CodexResetCredit): Promise<void> {
    if (busy || submittingId || !isUsable(item)) return
    if (!window.confirm(`确定使用该重置次数重置账号 ${displayName} 的限流窗口吗？`)) return
    const creditId = item.id.trim()
    setActionError('')
    setSubmittingId(creditId)
    try {
      await onConfirm(creditId)
      // 成功后由父组件关闭弹窗
    } catch (cause: unknown) {
      const detail = cause instanceof Error ? cause.message : String(cause)
      setActionError(detail || '重置失败')
    } finally {
      setSubmittingId('')
    }
  }

  const actionBusy = busy || Boolean(submittingId)

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="codex-reset-credits-dialog">
        <DialogHeader>
          <div>
            <DialogTitle>可用主动重置次数</DialogTitle>
            <DialogDescription>{displayName}</DialogDescription>
          </div>
        </DialogHeader>
        <DialogBody>
          {loading ? (
            <div className="empty-state">正在加载重置次数...</div>
          ) : error ? (
            <div className="notice danger-notice">{error}</div>
          ) : credits.length === 0 ? (
            <div className="empty-state">当前没有可用的重置次数</div>
          ) : (
            <>
              {actionError ? <div className="notice danger-notice">{actionError}</div> : null}
              <ul className="reset-credits-list">
                {credits.map((item, index) => {
                  const usable = isUsable(item)
                  const itemBusy = submittingId === item.id.trim()
                  return (
                    <li key={item.id || `credit-${index}`} className={`reset-credit-item${usable ? '' : ' disabled'}`}>
                      <div className="reset-credit-main">
                        <div className="reset-credit-title">{(item.title && item.title.trim()) || '主动重置次数'}</div>
                        {item.description && item.description.trim() ? (
                          <div className="reset-credit-desc">{item.description}</div>
                        ) : null}
                        <div className="reset-credit-meta">
                          <span>类型：{item.reset_type || '-'}</span>
                          <span>状态：{statusLabel(item)}</span>
                        </div>
                        <div className="reset-credit-time">
                          {formatDate(item.granted_at) ? <span>发放：{formatDate(item.granted_at)}</span> : null}
                          {formatDate(item.expires_at) ? <span>过期：{formatDate(item.expires_at)}</span> : null}
                        </div>
                        {!item.is_supported_by_plan ? (
                          <div className="reset-credit-unsupported">当前套餐不支持</div>
                        ) : null}
                      </div>
                      <button
                        type="button"
                        className="btn primary small"
                        disabled={actionBusy || !usable}
                        onClick={() => {
                          void handleReset(item)
                        }}
                      >
                        {itemBusy ? '重置中...' : '重置'}
                      </button>
                    </li>
                  )
                })}
              </ul>
            </>
          )}
        </DialogBody>
      </DialogContent>
    </Dialog>
  )
}

function formatDate(value?: string): string {
  if (!value) return ''
  const date = new Date(value)
  if (isNaN(date.getTime())) return ''
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}
