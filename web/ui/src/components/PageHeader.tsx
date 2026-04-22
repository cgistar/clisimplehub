interface PageHeaderProps {
  title: string
  description: string
  loading: boolean
  onRefresh: () => void
}

export default function PageHeader({ title, description, loading, onRefresh }: PageHeaderProps) {
  return (
    <div className="page-header">
      <div>
        <h1 className="page-title">{title}</h1>
        <p className="page-desc">{description}</p>
      </div>

      <div className="actions">
        <button className="btn" onClick={onRefresh} disabled={loading}>
          {loading ? '刷新中...' : '刷新'}
        </button>
      </div>
    </div>
  )
}
