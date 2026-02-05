/**
 * 主标签页切换模块
 */
import { loadKiroAccounts, renderKiroAccountCards, hideKiroAddAccountDropdown } from './kiroAccounts.js'

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

  if (tabName === 'home') {
    homeView.style.display = 'flex'
    kiroAccountsView.style.display = 'none'
  } else if (tabName === 'kiro-accounts') {
    homeView.style.display = 'none'
    kiroAccountsView.style.display = 'flex'

    // 加载账号列表
    await loadKiroAccounts()
    renderKiroAccountCards()

    // 设置点击页面其他地方关闭下拉菜单
    setTimeout(() => {
      document.addEventListener('click', handleClickOutside)
    }, 0)
  } else {
    // 移除点击事件监听
    document.removeEventListener('click', handleClickOutside)
  }
}

// 点击页面其他地方关闭下拉菜单
function handleClickOutside(event) {
  const dropdown = document.querySelector('.kiro-add-account-dropdown')
  if (dropdown && !dropdown.contains(event.target)) {
    hideKiroAddAccountDropdown()
  }
}
