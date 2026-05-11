import { useEffect, useMemo, useRef, useState } from 'react'
import { formatDateTime, numberOrDash } from '@/lib/format'
import type { EndpointGroup, EndpointInfo, HomePageData, RequestLogItem } from '@/types'

interface HomePageProps {
  data: HomePageData | null
  loading: boolean
  busyAction: string
  onActivateEndpoint: (interfaceType: string, endpointId: number) => void
}

function getInterfaceLabel(interfaceType: string): string {
  const normalized = (interfaceType || '').toLowerCase()
  if (normalized === 'claude') return 'Claude'
  if (normalized === 'codex') return 'Codex'
  if (normalized === 'gemini') return 'Gemini'
  if (normalized === 'chat') return 'Chat'
  if (!interfaceType) return '-'
  return interfaceType.charAt(0).toUpperCase() + interfaceType.slice(1)
}

function formatTokens(value?: number): string {
  const num = Number(value) || 0
  if (num >= 1000000) return `${(num / 1000000).toFixed(1)}m`
  if (num >= 1000) return `${(num / 1000).toFixed(1)}k`
  return String(num)
}

function endpointDisplayName(endpoint: EndpointInfo): string {
  return endpoint.providerName ? `${endpoint.providerName} - ${endpoint.name}` : endpoint.name
}

function sortEndpoints(endpoints: EndpointInfo[]): EndpointInfo[] {
  return [...endpoints].sort((left, right) => {
    const leftPriority = left.priority || 5
    const rightPriority = right.priority || 5
    if (leftPriority !== rightPriority) return rightPriority - leftPriority

    return endpointDisplayName(left).localeCompare(endpointDisplayName(right), 'zh-CN')
  })
}

function getInterfaceSortOrder(interfaceType: string): number {
  const normalized = (interfaceType || '').toLowerCase()
  if (normalized === 'claude') return 0
  if (normalized === 'codex') return 1
  if (normalized === 'gemini') return 2
  if (normalized === 'chat') return 3
  return 99
}

function sortEndpointGroups(groups: EndpointGroup[]): EndpointGroup[] {
  return [...groups]
    .map((group) => ({
      ...group,
      endpoints: sortEndpoints(group.endpoints || []),
    }))
    .sort((left, right) => {
      const leftOrder = getInterfaceSortOrder(left.interfaceType)
      const rightOrder = getInterfaceSortOrder(right.interfaceType)
      if (leftOrder !== rightOrder) return leftOrder - rightOrder
      return getInterfaceLabel(left.interfaceType).localeCompare(getInterfaceLabel(right.interfaceType), 'zh-CN')
    })
}

function isRealtimeStatus(status?: string): boolean {
  const normalized = (status || '').toLowerCase()
  return normalized === 'in_progress' || normalized === 'streaming' || normalized === 'pending'
}

function isFinishedStatus(status?: string): boolean {
  return !isRealtimeStatus(status)
}

function isIgnoredRequestLog(log?: RequestLogItem | null): boolean {
  const path = (log?.path || '').trim().toLowerCase()
  return path === '/favicon.ico'
}

function sortLogsByTimestamp(logs: RequestLogItem[]): RequestLogItem[] {
  return [...logs].sort((left, right) => {
    const leftTime = left.timestamp ? new Date(left.timestamp).getTime() : 0
    const rightTime = right.timestamp ? new Date(right.timestamp).getTime() : 0
    return rightTime - leftTime
  })
}

function upsertLog(logs: RequestLogItem[], nextLog: RequestLogItem, limit?: number): RequestLogItem[] {
  const nextId = String(nextLog.id || '')
  const withoutCurrent = logs.filter((item) => String(item.id || '') !== nextId)
  const merged = sortLogsByTimestamp([nextLog, ...withoutCurrent])
  if (!limit || merged.length <= limit) return merged
  return merged.slice(0, limit)
}

function formatRuntime(value?: number): string {
  const runtime = Number(value) || 0
  if (runtime >= 1000) return `${(runtime / 1000).toFixed(1)}s`
  return `${runtime}ms`
}

function formatElapsed(timestamp?: string): string {
  if (!timestamp) return '0s'
  const elapsed = Math.floor((Date.now() - new Date(timestamp).getTime()) / 1000)
  if (elapsed < 60) return `${elapsed}s`
  return `${Math.floor(elapsed / 60)}m${elapsed % 60}s`
}

export default function HomePage({ data, loading, busyAction, onActivateEndpoint }: HomePageProps) {
  const [activeTab, setActiveTab] = useState<string>('')
  const [selectedEndpointId, setSelectedEndpointId] = useState<string>('')
  const [realtimeLogs, setRealtimeLogs] = useState<RequestLogItem[]>([])
  const [historyLogs, setHistoryLogs] = useState<RequestLogItem[]>([])
  const [streamConnected, setStreamConnected] = useState<boolean>(false)
  const [, setTick] = useState(0)
  const tickRef = useRef<ReturnType<typeof setInterval> | null>(null)
  const groupedEndpoints = useMemo(() => sortEndpointGroups(data?.groupedEndpoints || []), [data?.groupedEndpoints])
  const recentLogs = useMemo(
    () => (data?.recentLogs || []).filter((log) => !isIgnoredRequestLog(log)),
    [data?.recentLogs],
  )
  const currentGroup = useMemo(() => {
    if (groupedEndpoints.length === 0) return null
    return groupedEndpoints.find((group) => group.interfaceType === activeTab) || groupedEndpoints[0]
  }, [activeTab, groupedEndpoints])
  const enabledEndpoints = useMemo(
    () => currentGroup?.endpoints.filter((endpoint) => endpoint.enabled) || [],
    [currentGroup],
  )
  const selectedEndpoint = currentGroup?.endpoints.find((endpoint) => String(endpoint.id) === selectedEndpointId)
  const switchBusy = selectedEndpoint ? busyAction === `endpoint:${selectedEndpoint.id}` : false

  useEffect(() => {
    if (groupedEndpoints.length === 0) {
      setActiveTab('')
      return
    }

    const hasActiveTab = groupedEndpoints.some((group) => group.interfaceType === activeTab)
    if (!hasActiveTab) {
      setActiveTab(groupedEndpoints[0].interfaceType)
    }
  }, [activeTab, groupedEndpoints])

  useEffect(() => {
    if (!currentGroup) {
      setSelectedEndpointId('')
      return
    }

    const nextSelected = currentGroup.activeEndpointId
      ? String(currentGroup.activeEndpointId)
      : String(enabledEndpoints[0]?.id || '')

    setSelectedEndpointId((current) => current === nextSelected ? current : nextSelected)
  }, [currentGroup, enabledEndpoints])

  useEffect(() => {
    setHistoryLogs(sortLogsByTimestamp(recentLogs.filter((log) => isFinishedStatus(log.status))).slice(0, 10))
  }, [recentLogs])

  const initialInProgressLogs = useMemo(
    () => (data?.inProgressLogs || []).filter((log) => !isIgnoredRequestLog(log)),
    [data?.inProgressLogs],
  )

  useEffect(() => {
    if (initialInProgressLogs.length === 0) return
    setRealtimeLogs((current) => {
      let merged = [...current]
      for (const log of initialInProgressLogs) {
        merged = upsertLog(merged, log, 20)
      }
      return merged
    })
  }, [initialInProgressLogs])

  useEffect(() => {
    tickRef.current = setInterval(() => setTick((n) => n + 1), 1000)
    return () => {
      if (tickRef.current) clearInterval(tickRef.current)
    }
  }, [])

  useEffect(() => {
    if (typeof window === 'undefined' || typeof window.EventSource === 'undefined') {
      return
    }

    const source = new window.EventSource('/web/sse')

    source.onopen = () => {
      setStreamConnected(true)
    }

    source.onerror = () => {
      setStreamConnected(false)
    }

    source.addEventListener('request_log', (event: MessageEvent<string>) => {
      try {
        const payload = JSON.parse(event.data) as RequestLogItem
        if (!payload || !payload.id) return
        if (isIgnoredRequestLog(payload)) return

        if (isRealtimeStatus(payload.status)) {
          setRealtimeLogs((current) => upsertLog(current, payload, 20))
          return
        }

        setRealtimeLogs((current) => current.filter((item) => String(item.id || '') !== String(payload.id)))
        setHistoryLogs((current) => upsertLog(current, payload, 10))
      } catch {
        // 忽略单条事件解析失败，避免中断整体连接
      }
    })

    return () => {
      setStreamConnected(false)
      source.close()
    }
  }, [])

  if (loading && !data) return <div className="card empty-state">正在加载主页数据...</div>
  if (!data) return <div className="card empty-state">暂无主页数据</div>

  return (
    <div className="grid">
      <section className="col-8 card">
        <div className="card-header">
          <div>
            <h2 className="card-title">端点概览</h2>
            <div className="card-subtitle">参考桌面版布局，按类型切换并查看端点今日统计</div>
          </div>
        </div>

        {groupedEndpoints.length === 0 ? (
          <div className="empty-state">当前没有配置任何 endpoint</div>
        ) : (
          <div className="home-endpoints-panel">
            <div className="home-endpoint-tabs" role="tablist" aria-label="端点类型">
              {groupedEndpoints.map((group) => {
                const active = currentGroup?.interfaceType === group.interfaceType
                return (
                  <button
                    key={group.interfaceType}
                    type="button"
                    className={`home-endpoint-tab${active ? ' active' : ''}`}
                    aria-selected={active}
                    onClick={() => setActiveTab(group.interfaceType)}
                  >
                    <span>{getInterfaceLabel(group.interfaceType)}</span>
                    <span className="home-endpoint-tab-count">{group.endpoints.length}</span>
                  </button>
                )
              })}
            </div>

            {currentGroup ? (
              <>
                <div className="home-endpoint-toolbar">
                  <div className="home-endpoint-toolbar-main">
                    <div className="home-endpoint-toolbar-title">
                      <strong>{getInterfaceLabel(currentGroup.interfaceType)}</strong>
                      <span className="muted">当前活跃：{currentGroup.activeEndpointName || '未设置'}</span>
                    </div>
                    <div className="home-endpoint-toolbar-controls">
                      <select
                        className="select home-endpoint-select"
                        value={selectedEndpointId}
                        onChange={(event) => setSelectedEndpointId(event.target.value)}
                      >
                        {enabledEndpoints.length === 0 ? (
                          <option value="">当前无可用端点</option>
                        ) : null}
                        {enabledEndpoints.map((endpoint) => (
                          <option key={endpoint.id} value={endpoint.id}>
                            {endpointDisplayName(endpoint)}
                          </option>
                        ))}
                      </select>
                      <button
                        className="btn primary"
                        disabled={!selectedEndpoint || selectedEndpoint.active || switchBusy}
                        onClick={() => {
                          if (!selectedEndpoint) return
                          onActivateEndpoint(currentGroup.interfaceType, selectedEndpoint.id)
                        }}
                      >
                        {switchBusy ? '切换中...' : '设为活跃'}
                      </button>
                    </div>
                  </div>
                  <div className="home-endpoint-toolbar-side">
                    <span className="badge muted">{enabledEndpoints.length}/{currentGroup.endpoints.length} 已启用</span>
                    <span className="badge info">{getInterfaceLabel(currentGroup.interfaceType)}</span>
                  </div>
                </div>

                <div className="home-endpoint-card-list">
                  {currentGroup.endpoints.map((endpoint) => (
                    <div className={`home-endpoint-card${endpoint.active ? ' active' : ''}${endpoint.enabled ? '' : ' disabled'}`} key={endpoint.id}>
                      <div className="home-endpoint-card-header">
                        <div className="home-endpoint-card-title-block">
                          <div className="row-wrap">
                            <h3 className="home-endpoint-card-title">
                              {endpointDisplayName(endpoint)}
                            </h3>
                            {endpoint.active ? <span className="badge success">当前活跃</span> : null}
                            <span className={`badge ${endpoint.enabled ? 'success' : 'warning'}`}>{endpoint.enabled ? '已启用' : '已禁用'}</span>
                          </div>
                          <div className="home-endpoint-url">{endpoint.apiUrl}</div>
                        </div>

                        <div className="actions">
                          <button
                            className={`btn${endpoint.active ? '' : ' primary'}`}
                            disabled={endpoint.active || !endpoint.enabled || busyAction === `endpoint:${endpoint.id}`}
                            onClick={() => onActivateEndpoint(currentGroup.interfaceType, endpoint.id)}
                          >
                            {endpoint.active ? '当前使用中' : busyAction === `endpoint:${endpoint.id}` ? '切换中...' : '切换'}
                          </button>
                        </div>
                      </div>

                      <div className="home-endpoint-meta">
                        <span className="meta-pill">provider: {endpoint.providerName || '-'}</span>
                        <span className="meta-pill">priority: {numberOrDash(endpoint.priority)}</span>
                        <span className="meta-pill">transformer: {endpoint.transformer || '-'}</span>
                        {endpoint.model ? <span className="meta-pill">model: {endpoint.model}</span> : null}
                      </div>

                      <div className="home-endpoint-stats">
                        <span className="home-endpoint-stat-item">📊 请求：{numberOrDash(endpoint.todayRequests ?? 0)}</span>
                        <span className={`home-endpoint-stat-item${(endpoint.todayErrors ?? 0) > 0 ? ' error' : ''}`}>
                          错误：{numberOrDash(endpoint.todayErrors ?? 0)}
                        </span>
                        <span className="home-endpoint-stat-item">
                          🔄 Token：{formatTokens((endpoint.todayInput ?? 0) + (endpoint.todayOutput ?? 0))}
                        </span>
                        <span className="home-endpoint-stat-subitem">
                          输入 {formatTokens(endpoint.todayInput)} / 输出 {formatTokens(endpoint.todayOutput)}
                        </span>
                      </div>
                    </div>
                  ))}
                </div>
              </>
            ) : null}
          </div>
        )}
      </section>

      <section className="col-4 card">
        <div className="card-header">
          <div>
            <h2 className="card-title">请求区</h2>
          </div>
          <span className={`badge ${streamConnected ? 'success' : 'muted'}`}>{streamConnected ? '已连接' : '未连接'}</span>
        </div>

        <div className="request-panel">
          <div className="request-section">
            <div className="request-section-header">
              <h3 className="request-section-title">实时请求</h3>
              <span className="badge info">{realtimeLogs.length}</span>
            </div>

            {realtimeLogs.length === 0 ? (
              <div className="empty-state request-empty">当前没有进行中的请求</div>
            ) : (
              <div className="request-list">
                {realtimeLogs.map((log, index) => (
                  <div className="request-card realtime" key={`${log.id ?? index}-${log.timestamp ?? index}`}>
                    <div className="request-card-header">
                      <div className="request-card-main">
                        <div className="request-card-title">{log.path || '-'}</div>
                        <div className="request-card-subtitle">
                          {(log.providerName || log.endpointName)
                            ? `${log.providerName || '-'} · ${log.endpointName || '-'}`
                            : (log.interfaceType || '-')}
                        </div>
                      </div>
                      <span className="badge info">进行中</span>
                    </div>

                    <div className="request-card-meta">
                      <span className="meta-pill">时间: {formatDateTime(log.timestamp)}</span>
                      <span className="meta-pill">耗时: {formatElapsed(log.timestamp)}</span>
                      {log.model ? <span className="meta-pill">model: {log.model}</span> : null}
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>

          <div className="request-section">
            <div className="request-section-header">
              <h3 className="request-section-title">最近请求</h3>
              <span className="badge muted">{historyLogs.length}</span>
            </div>

            {historyLogs.length === 0 ? (
              <div className="empty-state request-empty">暂无请求日志</div>
            ) : (
              <div className="request-list">
                {historyLogs.map((log, index) => {
                  const failed = (log.status || '').toLowerCase().startsWith('error') || (Number(log.statusCode) || 0) >= 400
                  return (
                    <div className="request-card" key={`${log.id ?? index}-${log.timestamp ?? index}`}>
                      <div className="request-card-header">
                        <div className="request-card-main">
                          <div className="request-card-title">{log.path || '-'}</div>
                          <div className="request-card-subtitle">
                            {(log.providerName || log.endpointName)
                              ? `${log.providerName || '-'} · ${log.endpointName || '-'}`
                              : (log.interfaceType || '-')}
                          </div>
                        </div>
                        <span className={`badge ${failed ? 'danger' : 'success'}`}>{failed ? '失败' : '成功'}</span>
                      </div>

                      <div className="request-card-meta">
                        <span className="meta-pill">时间: {formatDateTime(log.timestamp)}</span>
                        <span className="meta-pill">耗时: {formatRuntime(log.runTime)}</span>
                        {log.model ? <span className="meta-pill">model: {log.model}</span> : null}
                      </div>
                    </div>
                  )
                })}
              </div>
            )}
          </div>
        </div>
      </section>
    </div>
  )
}
