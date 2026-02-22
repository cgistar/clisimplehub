import { computed, ref } from 'vue'

export const CONSOLE_LOG_LEVELS = {
  DEBUG: 0,
  INFO: 1,
  WARN: 2,
  ERROR: 3
} as const

export type ConsoleLogLevel = (typeof CONSOLE_LOG_LEVELS)[keyof typeof CONSOLE_LOG_LEVELS]

type ConsoleLogEntry = {
  level: ConsoleLogLevel
  message: string
}

type ConsoleMethodName = 'debug' | 'log' | 'info' | 'warn' | 'error'

type ConsoleBridgeState = {
  installed: boolean
  wailsEventInstalled: boolean
  initializedLogWritten: boolean
}

const MAX_LOG_COUNT = 50
const BRIDGE_STATE_KEY = '__clisimplehub_console_bridge__'
const WAILS_GO_LOG_EVENT = 'app-log'
const LOG_DEDUP_WINDOW_MS = 1200

const panelVisible = ref(false)
const currentLogLevel = ref<ConsoleLogLevel>(CONSOLE_LOG_LEVELS.INFO)
const allLogs = ref<ConsoleLogEntry[]>([])
let lastLogSignature = ''
let lastLogTs = 0

function getConsoleBridgeState(): ConsoleBridgeState {
  const host = globalThis as typeof globalThis & { [BRIDGE_STATE_KEY]?: ConsoleBridgeState }
  if (host[BRIDGE_STATE_KEY]) return host[BRIDGE_STATE_KEY] as ConsoleBridgeState

  const state: ConsoleBridgeState = {
    installed: false,
    wailsEventInstalled: false,
    initializedLogWritten: false
  }
  host[BRIDGE_STATE_KEY] = state
  return state
}

function normalizeLogLevel(level: number): ConsoleLogLevel {
  if (!Number.isFinite(level)) return CONSOLE_LOG_LEVELS.INFO
  if (level <= CONSOLE_LOG_LEVELS.DEBUG) return CONSOLE_LOG_LEVELS.DEBUG
  if (level >= CONSOLE_LOG_LEVELS.ERROR) return CONSOLE_LOG_LEVELS.ERROR
  return level as ConsoleLogLevel
}

function stringifyConsoleArg(value: unknown): string {
  if (value instanceof Error) return value.stack || value.message || String(value)
  if (typeof value === 'string') return value
  if (typeof value === 'number' || typeof value === 'boolean' || typeof value === 'bigint') {
    return String(value)
  }
  if (value === null) return 'null'
  if (value === undefined) return 'undefined'

  try {
    return JSON.stringify(value)
  } catch {
    return String(value)
  }
}

function joinConsoleArgs(args: unknown[]): string {
  if (args.length === 0) return ''
  return args.map(stringifyConsoleArg).join(' ')
}

function mapConsoleMethodToLevel(method: ConsoleMethodName): number {
  if (method === 'debug') return CONSOLE_LOG_LEVELS.DEBUG
  if (method === 'warn') return CONSOLE_LOG_LEVELS.WARN
  if (method === 'error') return CONSOLE_LOG_LEVELS.ERROR
  return CONSOLE_LOG_LEVELS.INFO
}

function shouldDropDuplicate(level: ConsoleLogLevel, message: string): boolean {
  const now = Date.now()
  const signature = `${level}:${message}`
  if (signature !== lastLogSignature) {
    lastLogSignature = signature
    lastLogTs = now
    return false
  }
  if (now - lastLogTs <= LOG_DEDUP_WINDOW_MS) {
    lastLogTs = now
    return true
  }
  lastLogTs = now
  return false
}

function normalizeIncomingLogLevel(value: unknown): number {
  const parsed = Number(value)
  if (!Number.isFinite(parsed)) return CONSOLE_LOG_LEVELS.INFO
  return parsed
}

const filteredLogs = computed(() => allLogs.value.filter((log) => log.level >= currentLogLevel.value))
const renderedLogs = computed(() => filteredLogs.value.map((log) => log.message).join('\n'))

function appendLog(level: number, message: string): void {
  const normalizedLevel = normalizeLogLevel(Number(level))
  const normalizedMessage = String(message)
  if (!normalizedMessage) return
  if (shouldDropDuplicate(normalizedLevel, normalizedMessage)) return

  const next = {
    level: normalizedLevel,
    message: normalizedMessage
  }

  allLogs.value.push(next)
  if (allLogs.value.length > MAX_LOG_COUNT) {
    allLogs.value = allLogs.value.slice(-MAX_LOG_COUNT)
  }
}

function clearLogs(): void {
  allLogs.value = []
}

function setLogLevel(level: number | string): void {
  const parsed = typeof level === 'number' ? level : Number.parseInt(level, 10)
  if (!Number.isFinite(parsed)) return
  currentLogLevel.value = normalizeLogLevel(parsed)
}

function toggleBottomConsole(): void {
  panelVisible.value = !panelVisible.value
}

function closeBottomConsole(): void {
  panelVisible.value = false
}

function installConsoleBridge(): void {
  if (typeof console === 'undefined') return

  const state = getConsoleBridgeState()
  if (state.installed) return

  const methods: ConsoleMethodName[] = ['debug', 'log', 'info', 'warn', 'error']
  for (const method of methods) {
    const original = console[method].bind(console)
    console[method] = (...args: unknown[]) => {
      original(...args)
      appendLog(mapConsoleMethodToLevel(method), joinConsoleArgs(args))
    }
  }

  if (typeof window !== 'undefined') {
    window.addEventListener('error', (event: ErrorEvent) => {
      const message = event.error instanceof Error
        ? event.error.stack || event.error.message
        : event.message || String(event.error ?? 'Unknown error')
      appendLog(CONSOLE_LOG_LEVELS.ERROR, `[window.error] ${message}`)
    })

    window.addEventListener('unhandledrejection', (event: PromiseRejectionEvent) => {
      const reason = stringifyConsoleArg(event.reason)
      appendLog(CONSOLE_LOG_LEVELS.ERROR, `[unhandledrejection] ${reason}`)
    })
  }

  state.installed = true
}

function installWailsGoLogBridge(): void {
  if (typeof window === 'undefined') return

  const state = getConsoleBridgeState()
  if (state.wailsEventInstalled) return

  const eventsOn = window.runtime?.EventsOn as
    | ((eventName: string, callback: (...args: unknown[]) => void) => unknown)
    | undefined
  if (typeof eventsOn !== 'function') return

  eventsOn(WAILS_GO_LOG_EVENT, (payload: unknown) => {
    const raw = (payload && typeof payload === 'object') ? (payload as Record<string, unknown>) : {}
    const rawMessage = typeof raw.message === 'string'
      ? raw.message
      : stringifyConsoleArg(payload)
    if (!rawMessage) return

    const level = normalizeIncomingLogLevel(raw.level)
    appendLog(level, rawMessage)
  })

  state.wailsEventInstalled = true
}

function initConsole(): void {
  installConsoleBridge()
  installWailsGoLogBridge()

  const state = getConsoleBridgeState()
  if (state.initializedLogWritten) return

  state.initializedLogWritten = true
  appendLog(CONSOLE_LOG_LEVELS.INFO, 'Console initialized')
}

function fallbackCopyWithExecCommand(text: string): boolean {
  if (typeof document === 'undefined') return false

  const textarea = document.createElement('textarea')
  textarea.value = text
  textarea.setAttribute('readonly', '')
  textarea.style.position = 'fixed'
  textarea.style.opacity = '0'
  textarea.style.pointerEvents = 'none'
  document.body.appendChild(textarea)
  textarea.select()

  const copied = document.execCommand('copy')
  document.body.removeChild(textarea)
  return copied
}

async function copyRenderedLogs(): Promise<boolean> {
  const text = renderedLogs.value
  if (!text) return false

  try {
    if (typeof navigator !== 'undefined' && navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    // fallback below
  }

  return fallbackCopyWithExecCommand(text)
}

export function useConsole() {
  return {
    panelVisible,
    currentLogLevel,
    allLogs,
    filteredLogs,
    renderedLogs,
    appendLog,
    clearLogs,
    setLogLevel,
    toggleBottomConsole,
    closeBottomConsole,
    copyRenderedLogs,
    initConsole
  }
}
