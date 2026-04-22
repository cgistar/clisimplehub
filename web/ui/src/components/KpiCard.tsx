interface KpiCardProps {
  label: string
  value: string | number
}

export default function KpiCard({ label, value }: KpiCardProps) {
  return (
    <div className="kpi">
      <div className="kpi-label">{label}</div>
      <div className="kpi-value">{value}</div>
    </div>
  )
}
