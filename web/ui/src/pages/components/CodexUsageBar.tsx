import type { CodexUsageWindow } from '@/types'
import { formatRemainingSeconds } from '@/lib/format'

interface CodexUsageBarProps {
  label: string
  usage?: CodexUsageWindow
}

export default function CodexUsageBar({ label, usage }: CodexUsageBarProps) {
  if (!usage) return null

  const usedPercent = Math.max(0, Math.min(100, Number(usage.usedPercent || 0)))
  const tone = usedPercent >= 90 ? 'error' : usedPercent >= 70 ? 'warning' : 'primary'

  return (
    <div className="codex-usage-block">
      <div className="codex-usage-head">
        <span>{label}</span>
        <span>{usedPercent.toFixed(1)}%</span>
      </div>
      <div className="codex-usage-track">
        <div className={`codex-usage-fill codex-usage-fill-${tone}`} style={{ width: `${usedPercent}%` }} />
      </div>
      <div className="codex-usage-foot">重置剩余：{formatRemainingSeconds(usage.remainingSeconds)}</div>
    </div>
  )
}
