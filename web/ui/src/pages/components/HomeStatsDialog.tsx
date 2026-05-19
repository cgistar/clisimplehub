import { useEffect, useMemo, useState } from 'react'
import { toast } from 'sonner'
import { webApi } from '@/api/web'
import type { ApiError } from '@/api/client'
import type { EndpointStatsSummary, HourlyStatsSummary, InterfaceTypeStatsSummary, StatsRange } from '@/types'
import { Dialog, DialogBody, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'

interface HomeStatsDialogProps {
  open: boolean
  onClose: () => void
  onCleared: () => void | Promise<void>
  onAuthExpired: () => void
}

const rangeTabs: Array<{ key: StatsRange; label: string }> = [
  { key: 'today', label: '今日' },
  { key: 'yesterday', label: '昨日' },
  { key: 'week', label: '本周' },
  { key: 'month', label: '本月' },
  { key: 'all', label: '全部' },
]

function formatTokens(value?: number): string {
  const num = Number(value) || 0
  if (num >= 1000000) return `${(num / 1000000).toFixed(1)}m`
  if (num >= 1000) return `${(num / 1000).toFixed(1)}k`
  return String(num)
}

function getInterfaceLabel(interfaceType: string): string {
  const normalized = (interfaceType || '').toLowerCase()
  if (normalized === 'claude') return 'Claude'
  if (normalized === 'codex') return 'Codex'
  if (normalized === 'gemini') return 'Gemini'
  if (normalized === 'chat') return 'Chat'
  if (!interfaceType) return 'unknown'
  return interfaceType.charAt(0).toUpperCase() + interfaceType.slice(1)
}

function endpointDisplayName(endpoint: EndpointStatsSummary): string {
  return endpoint.providerName ? `${endpoint.providerName} - ${endpoint.endpointName || '-'}` : endpoint.endpointName || '-'
}

function toErrorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return String(error)
}

function isAuthError(error: unknown): boolean {
  const status = (error as ApiError)?.status
  return status === 401 || status === 403
}

function HourlyChart({ data }: { data: HourlyStatsSummary[] }) {
  const normalized = useMemo(() => {
    const buckets = Array.from({ length: 24 }, (_, hour) => ({
      hour,
      requestCount: 0,
      total: 0,
    }))
    for (const item of data || []) {
      const hour = Number(item.hour)
      if (hour < 0 || hour > 23) continue
      buckets[hour] = {
        hour,
        requestCount: Number(item.requestCount) || 0,
        total: Number(item.total) || 0,
      }
    }
    return buckets
  }, [data])

  const maxTotal = Math.max(1, ...normalized.map((item) => item.total))
  const maxRequests = Math.max(1, ...normalized.map((item) => item.requestCount))
  const hasData = normalized.some((item) => item.total > 0 || item.requestCount > 0)
  const width = 720
  const height = 220
  const chartTop = 20
  const chartBottom = 176
  const chartHeight = chartBottom - chartTop
  const step = width / normalized.length
  const barWidth = Math.max(8, step * 0.54)
  const points = normalized.map((item, index) => {
    const x = index * step + step / 2
    const y = chartBottom - (item.requestCount / maxRequests) * chartHeight
    return { x, y, item }
  })
  const polyline = points.map((point) => `${point.x},${point.y}`).join(' ')

  if (!hasData) {
    return <div className="stats-chart-empty">今日暂无小时统计数据</div>
  }

  return (
    <div className="stats-hourly-chart">
      <div className="stats-chart-legend">
        <span><i className="legend-token" />Token 总量</span>
        <span><i className="legend-request" />请求数</span>
      </div>
      <svg viewBox={`0 0 ${width} ${height}`} role="img" aria-label="今日每小时请求数与 Token 总量">
        <line className="stats-chart-axis" x1="0" y1={chartBottom} x2={width} y2={chartBottom} />
        {normalized.map((item, index) => {
          const x = index * step + (step - barWidth) / 2
          const barHeight = (item.total / maxTotal) * chartHeight
          return (
            <g key={item.hour}>
              <rect className="stats-chart-bar" x={x} y={chartBottom - barHeight} width={barWidth} height={barHeight} rx="4">
                <title>{`${String(item.hour).padStart(2, '0')}:00 Token ${formatTokens(item.total)} / 请求 ${item.requestCount}`}</title>
              </rect>
              {item.hour % 3 === 0 ? (
                <text className="stats-chart-label" x={index * step + step / 2} y={204} textAnchor="middle">
                  {String(item.hour).padStart(2, '0')}
                </text>
              ) : null}
            </g>
          )
        })}
        <polyline className="stats-chart-line" points={polyline} />
        {points.map((point) => (
          <circle className="stats-chart-point" key={point.item.hour} cx={point.x} cy={point.y} r="3.8">
            <title>{`${String(point.item.hour).padStart(2, '0')}:00 请求 ${point.item.requestCount}`}</title>
          </circle>
        ))}
      </svg>
    </div>
  )
}

export default function HomeStatsDialog({ open, onClose, onCleared, onAuthExpired }: HomeStatsDialogProps) {
  const [range, setRange] = useState<StatsRange>('today')
  const [statsData, setStatsData] = useState<InterfaceTypeStatsSummary[]>([])
  const [hourlyStats, setHourlyStats] = useState<HourlyStatsSummary[]>([])
  const [loading, setLoading] = useState<boolean>(false)
  const [refreshing, setRefreshing] = useState<boolean>(false)
  const [clearing, setClearing] = useState<boolean>(false)

  const totals = useMemo(() => statsData.reduce(
    (acc, item) => ({
      requestCount: acc.requestCount + (Number(item.requestCount) || 0),
      total: acc.total + (Number(item.total) || 0),
      inputTokens: acc.inputTokens + (Number(item.inputTokens) || 0),
      outputTokens: acc.outputTokens + (Number(item.outputTokens) || 0),
    }),
    { requestCount: 0, total: 0, inputTokens: 0, outputTokens: 0 },
  ), [statsData])

  async function loadStats(silent = false): Promise<void> {
    if (!open) return
    if (silent) {
      setRefreshing(true)
    } else {
      setLoading(true)
    }
    try {
      const [stats, hourly] = await Promise.all([
        webApi.getHomeStats(range),
        range === 'today' ? webApi.getTodayHourlyStats() : Promise.resolve([]),
      ])
      setStatsData(stats || [])
      setHourlyStats(hourly || [])
    } catch (error) {
      if (isAuthError(error)) {
        onAuthExpired()
        return
      }
      toast.error(`刷新统计失败：${toErrorMessage(error)}`)
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }

  useEffect(() => {
    if (!open) return
    void loadStats()
  }, [open, range])

  async function handleClear(): Promise<void> {
    if (!window.confirm(`确定清除“${rangeTabs.find((item) => item.key === range)?.label || range}”范围内的 TOKEN 统计吗？`)) {
      return
    }
    setClearing(true)
    try {
      const result = await webApi.clearHomeStats(range)
      toast.success(result.message || 'TOKEN 统计已清除')
      await loadStats(true)
      await onCleared()
    } catch (error) {
      if (isAuthError(error)) {
        onAuthExpired()
        return
      }
      toast.error(`清除 TOKEN 失败：${toErrorMessage(error)}`)
    } finally {
      setClearing(false)
    }
  }

  const busy = loading || refreshing || clearing

  return (
    <Dialog open={open} onOpenChange={(nextOpen) => {
      if (!nextOpen && !busy) onClose()
    }}>
      <DialogContent className="stats-dialog-card" closeDisabled={busy}>
        <DialogHeader>
          <div>
            <DialogTitle>Token 统计</DialogTitle>
            <DialogDescription>按接口类型和端点汇总请求与 Token 使用量</DialogDescription>
          </div>
        </DialogHeader>

        <DialogBody className="stats-dialog-body">
          <div className="stats-range-tabs" role="tablist" aria-label="统计时间范围">
            {rangeTabs.map((item) => (
              <button
                key={item.key}
                type="button"
                className={`stats-range-tab${range === item.key ? ' active' : ''}`}
                disabled={busy}
                aria-selected={range === item.key}
                onClick={() => setRange(item.key)}
              >
                {item.label}
              </button>
            ))}
          </div>

          {loading ? (
            <div className="stats-loading">
              <span className="spinner" />
              <span>正在加载统计...</span>
            </div>
          ) : (
            <>
              <div className="stats-summary-strip">
                <div><span>请求</span><strong>{totals.requestCount}</strong></div>
                <div><span>Token</span><strong>{formatTokens(totals.total)}</strong></div>
                <div><span>输入</span><strong>{formatTokens(totals.inputTokens)}</strong></div>
                <div><span>输出</span><strong>{formatTokens(totals.outputTokens)}</strong></div>
              </div>

              {range === 'today' ? (
                <section className="stats-section">
                  <div className="stats-section-header">
                    <h3>今日小时报</h3>
                    <span className="muted">请求数 + Token 总量</span>
                  </div>
                  <HourlyChart data={hourlyStats} />
                </section>
              ) : null}

              {statsData.length === 0 ? (
                <div className="empty-state stats-empty">当前范围暂无 Token 统计</div>
              ) : (
                <div className="stats-group-list">
                  {statsData.map((group) => (
                    <section className="stats-group-card" key={group.interfaceType || 'unknown'}>
                      <div className="stats-group-header">
                        <h3>{getInterfaceLabel(group.interfaceType)}</h3>
                        <span className="badge info">Token {formatTokens(group.total)}</span>
                      </div>
                      <div className="stats-metrics-grid">
                        <div><span>请求</span><strong>{group.requestCount || 0}</strong></div>
                        <div><span>输入</span><strong>{formatTokens(group.inputTokens)}</strong></div>
                        <div><span>缓存写入</span><strong>{formatTokens(group.cachedCreate)}</strong></div>
                        <div><span>缓存读取</span><strong>{formatTokens(group.cachedRead)}</strong></div>
                        <div><span>输出</span><strong>{formatTokens(group.outputTokens)}</strong></div>
                        <div><span>推理</span><strong>{formatTokens(group.reasoning)}</strong></div>
                      </div>
                      <div className="stats-table-wrap">
                        <table className="stats-table">
                          <thead>
                            <tr>
                              <th>端点</th>
                              {range === 'all' ? <th>日期</th> : null}
                              <th>请求</th>
                              <th>输入</th>
                              <th>缓存写入</th>
                              <th>缓存读取</th>
                              <th>输出</th>
                              <th>推理</th>
                              <th>总量</th>
                            </tr>
                          </thead>
                          <tbody>
                            {(group.endpoints || []).map((endpoint, index) => (
                              <tr key={`${endpoint.endpointId}-${endpoint.date || ''}-${index}`}>
                                <td>{endpointDisplayName(endpoint)}</td>
                                {range === 'all' ? <td>{endpoint.date || '-'}</td> : null}
                                <td>{endpoint.requestCount || 0}</td>
                                <td>{formatTokens(endpoint.inputTokens)}</td>
                                <td>{formatTokens(endpoint.cachedCreate)}</td>
                                <td>{formatTokens(endpoint.cachedRead)}</td>
                                <td>{formatTokens(endpoint.outputTokens)}</td>
                                <td>{formatTokens(endpoint.reasoning)}</td>
                                <td>{formatTokens(endpoint.total)}</td>
                              </tr>
                            ))}
                          </tbody>
                        </table>
                      </div>
                    </section>
                  ))}
                </div>
              )}
            </>
          )}
        </DialogBody>

        <DialogFooter>
          <button type="button" className="btn" disabled={loading || clearing || refreshing} onClick={() => void loadStats(true)}>
            {refreshing ? '刷新中...' : '刷新'}
          </button>
          <button type="button" className="btn danger" disabled={loading || refreshing || clearing} onClick={() => void handleClear()}>
            {clearing ? '清除中...' : '清除 TOKEN'}
          </button>
          <button type="button" className="btn" disabled={busy} onClick={onClose}>
            关闭
          </button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
