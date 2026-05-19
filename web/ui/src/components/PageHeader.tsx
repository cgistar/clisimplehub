import type { ReactNode } from 'react'

interface PageHeaderProps {
  title: string
  description: string
  loading: boolean
  onRefresh: () => void
  showRefresh?: boolean
  extraActions?: ReactNode
}

export default function PageHeader({ title, description, loading, onRefresh, showRefresh = true, extraActions }: PageHeaderProps) {
  return (
    <div className="page-header">
      <div>
        <h1 className="page-title">{title}</h1>
        <p className="page-desc">{description}</p>
      </div>

      {showRefresh || extraActions ? (
        <div className="actions">
          {showRefresh ? (
            <button className="btn" onClick={onRefresh} disabled={loading}>
              {loading ? '刷新中...' : '刷新'}
            </button>
          ) : null}
          {extraActions}
        </div>
      ) : null}
    </div>
  )
}
