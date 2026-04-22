import type { RouteKey } from '@/types'
import NavLink from './NavLink'

interface TopbarProps {
  route: RouteKey
  onNavigate: (route: RouteKey) => void
  onLogout: () => void | Promise<void>
}

export default function Topbar({ route, onNavigate, onLogout }: TopbarProps) {
  return (
    <header className="topbar">
      <div className="brand">
        <div className="brand-badge">CSH</div>
        <div>
          <div className="brand-title">Cli Simple Hub</div>
          <div className="brand-subtitle">Headless Server Web Console</div>
        </div>
      </div>

      <nav className="nav-links">
        <NavLink active={route === 'home'} onClick={() => onNavigate('home')}>
          主页
        </NavLink>
        <NavLink active={route === 'codex'} onClick={() => onNavigate('codex')}>
          Codex
        </NavLink>
        <NavLink active={route === 'settings'} onClick={() => onNavigate('settings')}>
          设置
        </NavLink>
      </nav>

      <div className="topbar-actions">
        <button className="btn danger" onClick={onLogout}>
          退出登录
        </button>
      </div>
    </header>
  )
}
