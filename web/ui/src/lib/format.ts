export function formatDateTime(value?: string): string {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString('zh-CN', { hour12: false })
}

export function numberOrDash(value: unknown): string {
  if (value === null || value === undefined) return '-'
  return String(value)
}

export function maskSecret(value?: string): string {
  if (!value) return '(未设置)'
  if (value.length <= 8) return `${value.slice(0, 2)}***${value.slice(-1)}`
  return `${value.slice(0, 4)}***${value.slice(-4)}`
}

export function formatTokenCount(value: unknown): string {
  const num = Number(value) || 0
  if (num >= 1_000_000) return `${(num / 1_000_000).toFixed(1)}M`
  if (num >= 1_000) return `${(num / 1_000).toFixed(1)}K`
  return String(num)
}

export function formatRemainingSeconds(value: unknown): string {
  const seconds = Number(value) || 0
  if (seconds <= 0) return '即将重置'

  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const minutes = Math.ceil((seconds % 3600) / 60)

  if (days > 0) return `${days}d ${hours}h`
  if (hours > 0) return `${hours}h ${minutes}m`
  return `${minutes}m`
}

export function formatCompactRemainingSeconds(value: unknown): string {
  const seconds = Number(value) || 0
  if (seconds <= 0) return '0'

  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const minutes = Math.ceil((seconds % 3600) / 60)

  if (days > 0) return `${days}d${hours}h`
  if (hours > 0) return `${hours}h${minutes}m`
  return `${minutes}m`
}

export async function copyToClipboard(text: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text)
    return
  }

  const textarea = document.createElement('textarea')
  textarea.value = text
  textarea.setAttribute('readonly', 'true')
  textarea.style.position = 'fixed'
  textarea.style.opacity = '0'
  document.body.appendChild(textarea)
  textarea.select()
  document.execCommand('copy')
  document.body.removeChild(textarea)
}
