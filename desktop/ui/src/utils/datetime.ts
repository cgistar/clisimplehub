export function toTimestampMs(value: unknown): number {
  if (value == null) return Number.NaN

  if (typeof value === 'number') {
    return Number.isFinite(value) ? value : Number.NaN
  }

  if (value instanceof Date) {
    const ms = value.getTime()
    return Number.isFinite(ms) ? ms : Number.NaN
  }

  if (typeof value !== 'string') return Number.NaN
  const raw = value.trim()
  if (!raw) return Number.NaN

  const direct = Date.parse(raw)
  if (Number.isFinite(direct)) return direct

  // Common backend format: "YYYY-MM-DD HH:mm:ss"
  if (raw.includes(' ') && !raw.includes('T')) {
    const normalized = raw.replace(' ', 'T')
    const normalizedMs = Date.parse(normalized)
    if (Number.isFinite(normalizedMs)) return normalizedMs
  }

  return Number.NaN
}

export function formatTimeSafe(value: unknown, fallback = '-'): string {
  if (typeof value === 'string') {
    const raw = value.trim()
    if (!raw) return fallback
    // Keep already-formatted time strings (e.g. "11:31:28")
    if (/^\d{2}:\d{2}:\d{2}$/.test(raw)) return raw
  }

  const ms = toTimestampMs(value)
  if (!Number.isFinite(ms)) {
    if (typeof value === 'string' && value.trim()) return value.trim()
    return fallback
  }

  try {
    return new Intl.DateTimeFormat('default', {
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit'
    }).format(new Date(ms))
  } catch {
    return fallback
  }
}

export function formatDateTimeSafe(value: unknown, fallback = '-'): string {
  if (typeof value === 'string') {
    const raw = value.trim()
    if (!raw) return fallback
    if (/^\d{2}:\d{2}:\d{2}$/.test(raw)) return raw
  }

  const ms = toTimestampMs(value)
  if (!Number.isFinite(ms)) {
    if (typeof value === 'string' && value.trim()) return value.trim()
    return fallback
  }

  try {
    return new Intl.DateTimeFormat('default', {
      dateStyle: 'medium',
      timeStyle: 'medium'
    }).format(new Date(ms))
  } catch {
    return fallback
  }
}
