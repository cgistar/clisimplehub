/**
 * 主标签页切换模块
 */
import { loadKiroAccounts, renderKiroAccountCards, hideKiroAddAccountDropdown } from './kiroAccounts.js'
import { loadGrokAccounts, renderGrokAccountCards } from './grokAccounts.js'

let currentMainTab = 'home'

export function getCurrentMainTab() {
  return currentMainTab
}

export async function switchMainTab(tabName) {
  currentMainTab = tabName

  // 更新标签按钮状态
  document.querySelectorAll('.header-tab').forEach((btn) => {
    btn.classList.toggle('active', btn.dataset.tab === tabName)
  })

  // 切换视图
  const homeView = document.getElementById('homeView')
  const kiroAccountsView = document.getElementById('kiroAccountsView')
  const grokAccountsView = document.getElementById('grokAccountsView')

  homeView.style.display = 'none'
  kiroAccountsView.style.display = 'none'
  if (grokAccountsView) grokAccountsView.style.display = 'none'

  document.removeEventListener('click', handleClickOutside)

  if (tabName === 'home') {
    homeView.style.display = 'flex'
  } else if (tabName === 'kiro-accounts') {
    kiroAccountsView.style.display = 'flex'
    await loadKiroAccounts()
    renderKiroAccountCards()
    setTimeout(() => {
      document.addEventListener('click', handleClickOutside)
    }, 0)
  } else if (tabName === 'grok-accounts') {
    if (grokAccountsView) grokAccountsView.style.display = 'flex'
    await loadGrokAccounts()
    renderGrokAccountCards()
  }
}

// 点击页面其他地方关闭下拉菜单
function handleClickOutside(event) {
  const dropdown = document.querySelector('.kiro-add-account-dropdown')
  if (dropdown && !dropdown.contains(event.target)) {
    hideKiroAddAccountDropdown()
  }
}
