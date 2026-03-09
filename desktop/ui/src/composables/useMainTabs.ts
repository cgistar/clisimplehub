import { ref } from 'vue'
import { useConsole } from '@/composables/useConsole'

export type MainTabName = 'home' | 'kiro-accounts' | 'codex-accounts' | 'clash' | 'settings'

const activeTab = ref<MainTabName>('home')
const showKiro = ref(false)
const showCodex = ref(false)
const showClash = ref(false)
const { closeBottomConsole } = useConsole()

function setTab(tabName: MainTabName): void {
  activeTab.value = tabName
}

function setTabVisibility({
  kiro,
  codex,
  clash
}: {
  kiro?: boolean
  codex?: boolean
  clash?: boolean
}): void {
  if (kiro !== undefined) showKiro.value = !!kiro
  if (codex !== undefined) showCodex.value = !!codex
  if (clash !== undefined) showClash.value = !!clash
}

async function switchMainTab(tabName: MainTabName): Promise<void> {
  setTab(tabName)

  if (tabName !== 'home') {
    closeBottomConsole()
  }

  if (tabName === 'home') {
    window.dispatchEvent(new Event('home:visible'))
  }
}

export function useMainTabs() {
  return {
    activeTab,
    showKiro,
    showCodex,
    showClash,
    setTab,
    setTabVisibility,
    switchMainTab
  }
}
