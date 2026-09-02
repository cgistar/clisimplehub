import type { CodexUsageWindow } from '@/types'
import { formatCompactRemainingSeconds } from '@/lib/format'
import { RefreshIcon } from '@/components/icons'

interface CodexUsageBarProps {
  label?: string
  usage?: CodexUsageWindow
  refreshable?: boolean
  refreshDisabled?: boolean
  refreshTitle?: string
  onRefresh?: () => void
}

export default function CodexUsageBar({
  label,
  usage,
  refreshable = false,
  refreshDisabled = false,
  refreshTitle = '刷新',
  onRefresh,
}: CodexUsageBarProps) {
  if (!usage && !refreshable) return null

  const usedPercent = Math.max(0, Math.min(100, Number(usage?.usedPercent || 0)))
  const tone = usedPercent >= 90 ? 'error' : usedPercent >= 70 ? 'warning' : 'primary'
  const remaining = formatCompactRemainingSeconds(usage?.remainingSeconds)
  const headText = label ? `${label}: ${remaining}` : remaining

  return (
    <div className="codex-usage-block">
      <div className="codex-usage-head">
        <span className="codex-usage-label">{headText}</span>
        {refreshable ? (
          <button
            className="codex-usage-refresh-btn"
            type="button"
            title={refreshTitle}
            aria-label={refreshTitle}
            disabled={refreshDisabled}
            onClick={onRefresh}
          >
            <RefreshIcon />
          </button>
        ) : null}
        <span className="codex-usage-percent">{usedPercent.toFixed(1)}%</span>
      </div>
      <div className="codex-usage-track">
        <div className={`codex-usage-fill codex-usage-fill-${tone}`} style={{ width: `${usedPercent}%` }} />
      </div>
    </div>
  )
}
