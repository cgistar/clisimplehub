import type { ReactNode } from 'react'

function IconBase({ children }: { children: ReactNode }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      {children}
    </svg>
  )
}

export function PowerIcon() {
  return (
    <IconBase>
      <path d="M12 2v10" />
      <path d="M18.4 6.6a9 9 0 1 1-12.8 0" />
    </IconBase>
  )
}

export function RefreshIcon() {
  return (
    <IconBase>
      <path d="M21 2v6h-6" />
      <path d="M3 22v-6h6" />
      <path d="M20 11a8 8 0 0 0-14.85-4M4 13a8 8 0 0 0 14.85 4" />
    </IconBase>
  )
}

export function ActivityIcon() {
  return (
    <IconBase>
      <path d="M22 12h-4l-3 8-4-16-3 8H2" />
    </IconBase>
  )
}

export function CopyIcon() {
  return (
    <IconBase>
      <rect x="9" y="9" width="13" height="13" rx="2" />
      <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
    </IconBase>
  )
}

export function EditIcon() {
  return (
    <IconBase>
      <path d="M12 20h9" />
      <path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4Z" />
    </IconBase>
  )
}

export function TrashIcon() {
  return (
    <IconBase>
      <path d="M3 6h18" />
      <path d="M8 6V4a1 1 0 0 1 1-1h6a1 1 0 0 1 1 1v2" />
      <path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6" />
      <path d="M10 11v6M14 11v6" />
    </IconBase>
  )
}
