import type { XaiAccount, XaiQuotaWindow } from '@/types'
import { getXaiExpireInfo, getXaiStatus } from '@/lib/xai'
import { ActivityIcon, CopyIcon, EditIcon, GaugeIcon, PowerIcon, RefreshIcon, TrashIcon } from '@/components/icons'

interface XaiAccountCardProps {
  account: XaiAccount
  busyAction: string
  onActivate: (accountId: string) => void
  onProbeStream: (accountId: string) => void
  onRefreshQuota: (accountId: string) => void
  onRefreshToken: (accountId: string) => void
  onCopy: (account: XaiAccount) => void
  onEdit: (account: XaiAccount) => void
  onDelete: (accountId: string) => void
}

function poolLabel(pool?: string): string {
  switch (String(pool || '').toLowerCase()) {
    case 'basic':
      return 'Basic'
    case 'super':
      return 'Super'
    case 'heavy':
      return 'Heavy'
    default:
      return ''
  }
}

function formatQuotaPart(label: string, win?: XaiQuotaWindow): string {
  if (!win || win.total == null || win.total <= 0) return ''
  const remaining = Math.max(0, Number(win.remaining ?? 0))
  return `${label} ${remaining}/${win.total}`
}

function quotaSummary(account: XaiAccount): string {
  const q = account.quota
  if (!q) return ''
  return [
    formatQuotaPart('Fast', q.fast),
    formatQuotaPart('Auto', q.auto),
    formatQuotaPart('Expert', q.expert),
    formatQuotaPart('Heavy', q.heavy),
  ]
    .filter(Boolean)
    .join(' · ')
}

export default function XaiAccountCard({
  account,
  busyAction,
  onActivate,
  onProbeStream,
  onRefreshQuota,
  onRefreshToken,
  onCopy,
  onEdit,
  onDelete,
}: XaiAccountCardProps) {
  const status = getXaiStatus(account)
  const displayName = account.email || account.subject || account.id || '(未命名账号)'
  const localId = account.id || ''
  const canActivate = Boolean(localId) && !account.isActive && account.status !== 'banned'
  const canRefreshQuota = Boolean(String(account.sso || '').trim())
  const activateBusy = busyAction === `xai:activate:${localId}`
  const probeBusy = busyAction === `xai:probe:${localId}`
  const quotaBusy = busyAction === `xai:quota:${localId}`
  const refreshBusy = busyAction === `xai:refresh:${localId}`
  const deleteBusy = busyAction === `xai:delete:${localId}`
  const actionBusy = activateBusy || probeBusy || quotaBusy || refreshBusy || deleteBusy
  const pool = poolLabel(account.pool)
  const quotaText = quotaSummary(account)

  const expireInfo = getXaiExpireInfo(account)

  return (
    <article className={`codex-account-card xai-account-card${account.isActive ? ' active' : ''}`}>
      <div className="codex-account-card-header">
        <div className="codex-header-main">
          <h3 className="list-item-title no-margin codex-card-title" title={displayName}>
            {displayName}
          </h3>
          <span className={`badge ${status.variant}`}>{status.label}</span>
        </div>

        <div className="codex-card-tags">
          {pool ? <span className="badge info">{pool}</span> : null}
          {account.authKind === 'api_key' ? <span className="badge info">API Key</span> : null}
          {account.enabled === false ? <span className="badge danger">已禁用</span> : null}
          {account.websockets ? <span className="badge success">WS</span> : null}
          {account.usingApi ? <span className="badge warning">官方API</span> : null}
          {account.sso ? <span className="badge info">SSO</span> : null}
          {account.isActive ? <span className="badge success">正在使用</span> : null}
        </div>
      </div>

      {quotaText || account.proxyUrl ? (
        <div className="codex-account-card-body">
          <div className="codex-account-meta-grid">
            {quotaText ? <div className="meta-pill codex-full-span">{quotaText}</div> : null}
            {account.proxyUrl ? <div className="meta-pill codex-full-span">代理：{account.proxyUrl}</div> : null}
          </div>
        </div>
      ) : null}

      <div className="codex-account-card-footer">
        <div
          className={`codex-expire-text${expireInfo.expired ? ' danger-text' : ''}`}
          title={account.expiresAt || undefined}
        >
          {expireInfo.text}
        </div>

        <div className="codex-card-actions">
          {canActivate ? (
            <button
              className="codex-action-btn codex-action-primary"
              type="button"
              title={activateBusy ? '激活中...' : '激活'}
              aria-label="激活"
              disabled={actionBusy}
              onClick={() => onActivate(localId)}
            >
              <PowerIcon />
            </button>
          ) : null}
          <button
            className="codex-action-btn"
            type="button"
            title={probeBusy ? '连通测试中...' : '连通测试'}
            aria-label="连通测试"
            disabled={actionBusy || !localId}
            onClick={() => onProbeStream(localId)}
          >
            <ActivityIcon />
          </button>
          <button
            className="codex-action-btn"
            type="button"
            title={
              !canRefreshQuota
                ? '需要 SSO Cookie 才能刷新额度'
                : quotaBusy
                  ? '刷新额度中...'
                  : '刷新额度'
            }
            aria-label="刷新额度"
            disabled={actionBusy || !localId || !canRefreshQuota}
            onClick={() => onRefreshQuota(localId)}
          >
            <GaugeIcon />
          </button>
          <button
            className="codex-action-btn"
            type="button"
            title={refreshBusy ? '刷新 Token 中...' : '刷新 Token'}
            aria-label="刷新 Token"
            disabled={actionBusy || !localId}
            onClick={() => onRefreshToken(localId)}
          >
            <RefreshIcon />
          </button>
          <button
            className="codex-action-btn"
            type="button"
            title="复制 auth.json"
            aria-label="复制"
            disabled={actionBusy}
            onClick={() => onCopy(account)}
          >
            <CopyIcon />
          </button>
          <button
            className="codex-action-btn"
            type="button"
            title="编辑"
            aria-label="编辑"
            disabled={actionBusy}
            onClick={() => onEdit(account)}
          >
            <EditIcon />
          </button>
          <button
            className="codex-action-btn codex-action-danger"
            type="button"
            title={deleteBusy ? '删除中...' : '删除'}
            aria-label="删除"
            disabled={actionBusy || !localId}
            onClick={() => {
              if (window.confirm(`确定删除 xAI 账号 ${displayName} 吗？`)) {
                onDelete(localId)
              }
            }}
          >
            <TrashIcon />
          </button>
        </div>
      </div>
    </article>
  )
}
