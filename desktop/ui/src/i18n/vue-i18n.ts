import { createI18n } from 'vue-i18n'
import en from './en'
import zhCN from './zh-CN'

const messages = {
  en,
  'zh-CN': zhCN,
  zh: zhCN
}

function normalizeLanguage(lang: string): keyof typeof messages {
  if (lang === 'zh') return 'zh-CN'
  if (lang in messages) return lang as keyof typeof messages
  return 'en'
}

const savedLanguage = normalizeLanguage(localStorage.getItem('language') || 'en')

export const i18n = createI18n({
  legacy: false,
  locale: savedLanguage,
  fallbackLocale: 'en',
  missingWarn: false,
  fallbackWarn: false,
  messages,
  globalInjection: true
})

export function t(key: string): string {
  return String(i18n.global.t(key))
}

export function setLanguage(lang: string): void {
  const normalized = normalizeLanguage(lang)
  i18n.global.locale.value = normalized
  localStorage.setItem('language', normalized)
}

export function getLanguage(): string {
  return String(i18n.global.locale.value)
}

export function getAvailableLanguages(): Array<{ code: 'en' | 'zh-CN'; name: string }> {
  return [
    { code: 'en', name: 'English' },
    { code: 'zh-CN', name: '简体中文' }
  ]
}
