import type { RouteKey } from '@/types'

export const ROUTES: Record<RouteKey, string> = {
  home: '/web',
  codex: '/web/codex',
  xai: '/web/xai',
  proxy: '/web/proxy',
  settings: '/web/settings',
}

export function routeFromPath(pathname: string): RouteKey {
  if (pathname === '/web/codex' || pathname === '/web/codex/') return 'codex'
  if (pathname === '/web/xai' || pathname === '/web/xai/') return 'xai'
  if (pathname === '/web/proxy' || pathname === '/web/proxy/') return 'proxy'
  if (pathname === '/web/settings' || pathname === '/web/settings/') return 'settings'
  return 'home'
}
