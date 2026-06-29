import type { CodexAccount } from '@/types'
import { formatDateTime, formatTokenCount } from '@/lib/format'
import { getCodexPlanLabel, getCodexStatus, getCodexSubscriptionActiveUntil, getExpireInfo } from '@/lib/codex'
import { ActivityIcon, CopyIcon, EditIcon, PowerIcon, RefreshIcon, TrashIcon } from '@/components/icons'
import CodexUsageBar from './CodexUsageBar'

interface CodexAccountCardProps {
  account: CodexAccount
  busyAction: string
  onActivate: (accountId: string) => void
  onRefreshToken: (accountId: string) => void
  onFetchUsage: (accountId: string) => void
  onFetchPrimaryUsage: (accountId: string) => void
  onResetCredit: (accountId: string) => void
  onCopy: (account: CodexAccount) => void
  onEdit: (account: CodexAccount) => void
  onDelete: (accountId: string) => void
}

export default function CodexAccountCard({
  account,
  busyAction,
  onActivate,
  onRefreshToken,
  onFetchUsage,
  onFetchPrimaryUsage,
  onResetCredit,
  onCopy,
  onEdit,
  onDelete,
}: CodexAccountCardProps) {
  const status = getCodexStatus(account)
  const expireInfo = getExpireInfo(account)
  const displayName = account.email || account.accountId || '(未命名账号)'
  const planLabel = getCodexPlanLabel(account.planType)
  const localId = account.id || ''
  const canActivate = Boolean(localId) && !account.isActive && account.status !== 'banned' && account.status !== 'reused'
  const activateBusy = busyAction === `codex:activate:${localId}`
  const refreshBusy = busyAction === `codex:refresh:${localId}`
  const usageBusy = busyAction === `codex:usage:${localId}`
  const primaryUsageBusy = busyAction === `codex:usage-primary:${localId}`
  const resetBusy = busyAction === `codex:reset:${localId}`
  const deleteBusy = busyAction === `codex:delete:${localId}`
  const actionBusy = activateBusy || refreshBusy || usageBusy || primaryUsageBusy || resetBusy || deleteBusy
  const subscriptionActiveUntil = getCodexSubscriptionActiveUntil(account.idToken)
  const resetCreditsAvailableCount = Number(account.codexUsage?.resetCreditsAvailableCount || 0)

  function confirmResetCredit() {
    if (actionBusy || resetCreditsAvailableCount <= 0) return
    if (window.confirm(`确定重置账号 ${displayName} 的周限吗？当前可用次数：${resetCreditsAvailableCount}`)) {
      onResetCredit(localId)
    }
  }

  return (
    <article className={`codex-account-card${account.isActive ? ' active' : ''}`}>
      <div className="codex-account-card-header">
        <div className="codex-header-main">
          <h3 className="list-item-title no-margin codex-card-title" title={displayName}>
            {displayName}
          </h3>
          <span className={`badge ${status.variant}`}>{status.label}</span>
        </div>

        <div className="codex-card-tags">
          {planLabel ? <span className="badge info">{planLabel}</span> : null}
          {resetCreditsAvailableCount > 0 ? (
            <span className="badge success" title="rate_limit_reset_credits.available_count">
              {resetCreditsAvailableCount}
            </span>
          ) : null}
          {!account.refreshToken ? <span className="badge warning">临时 Token</span> : null}
          {account.enabled === false ? <span className="badge danger">已禁用</span> : null}
          {account.websockets ? <span className="badge success">WS</span> : null}
          {account.isActive ? <span className="badge success">正在使用</span> : null}
        </div>
      </div>

      <div className="codex-account-card-body">
        <CodexUsageBar
          label="5 小时限"
          usage={account.codexUsage?.primary}
          refreshable
          refreshDisabled={actionBusy || !localId}
          refreshTitle={primaryUsageBusy ? '刷新 5 小时用量中...' : '刷新 5 小时用量'}
          onRefresh={() => onFetchPrimaryUsage(localId)}
        />
        <CodexUsageBar
          label="周限"
          usage={account.codexUsage?.secondary}
          refreshable
          refreshDisabled={actionBusy || !localId || resetCreditsAvailableCount <= 0}
          refreshTitle={resetCreditsAvailableCount > 0 ? (resetBusy ? '重置周限中...' : '重置周限') : '没有可用重置次数'}
          onRefresh={confirmResetCredit}
        />

        <div className="codex-account-meta-grid">
          <div className="meta-pill">
            今日请求: {Number(account.todayRequests || 0)}/{formatTokenCount(account.todayTotalTokens)}
            {' '}缓存: {formatTokenCount(account.todayCachedTokens)}
            {' '}推理: {formatTokenCount(account.todayReasoningTokens)}
          </div>
          <div className="meta-pill">有效至{formatDateTime(subscriptionActiveUntil)}</div>
          {account.proxyUrl ? <div className="meta-pill codex-full-span">代理：{account.proxyUrl}</div> : null}
        </div>
      </div>

      <div className="codex-account-card-footer">
        <div className={`codex-expire-text${expireInfo.expired ? ' danger-text' : ''}`}>{expireInfo.text}</div>

        <div className="codex-card-actions">
          {canActivate ? (
            <button
              className="codex-action-btn codex-action-primary"
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
            title={refreshBusy ? '刷新 Token 中...' : '刷新 Token'}
            aria-label="刷新 Token"
            disabled={actionBusy || !account.refreshToken}
            onClick={() => onRefreshToken(localId)}
          >
            <RefreshIcon />
          </button>
          <button
            className="codex-action-btn"
            title={usageBusy ? '获取用量中...' : '获取用量'}
            aria-label="获取用量"
            disabled={actionBusy}
            onClick={() => onFetchUsage(localId)}
          >
            <ActivityIcon />
          </button>
          <button className="codex-action-btn" title="复制" aria-label="复制" disabled={actionBusy} onClick={() => onCopy(account)}>
            <CopyIcon />
          </button>
          <button className="codex-action-btn" title="编辑" aria-label="编辑" disabled={actionBusy} onClick={() => onEdit(account)}>
            <EditIcon />
          </button>
          <button
            className="codex-action-btn codex-action-danger"
            disabled={actionBusy}
            title={deleteBusy ? '删除中...' : '删除'}
            aria-label="删除"
            onClick={() => {
              if (window.confirm(`确定删除账号 ${displayName} 吗？`)) {
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
