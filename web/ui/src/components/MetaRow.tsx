interface MetaRowProps {
  label: string
  value: string | number | boolean
}

export default function MetaRow({ label, value }: MetaRowProps) {
  return (
    <div className="list-item">
      <div className="meta-label">{label}</div>
      <div>{value}</div>
    </div>
  )
}
