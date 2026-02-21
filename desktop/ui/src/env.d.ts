/// <reference types="vite/client" />

declare module '*.vue' {
  import type { DefineComponent } from 'vue'
  const component: DefineComponent<Record<string, never>, Record<string, never>, unknown>
  export default component
}

declare global {
  interface Window {
    runtime?: {
      BrowserOpenURL?: (url: string) => void
      EventsOn?: (eventName: string, callback: (...args: any[]) => void) => (() => void) | void
      EventsOff?: (eventName: string, ...additionalEventNames: string[]) => void
    }
  }
}

export {}
