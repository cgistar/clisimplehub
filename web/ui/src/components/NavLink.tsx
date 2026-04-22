import type { ReactNode } from 'react'

interface NavLinkProps {
  active: boolean
  onClick: () => void
  children: ReactNode
}

export default function NavLink({ active, onClick, children }: NavLinkProps) {
  return (
    <button type="button" className={`nav-link${active ? ' active' : ''}`} onClick={onClick}>
      {children}
    </button>
  )
}
