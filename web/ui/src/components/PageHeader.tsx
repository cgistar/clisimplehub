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
  void title
  void description
  return (
    <div className="page-header">
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
