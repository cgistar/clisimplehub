<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { NAlert, NButton, NInput, NInputGroup, NModal, NRadioButton, NRadioGroup, NSelect } from 'naive-ui'
import { useI18n } from 'vue-i18n'
import { endpointApi } from '@/api/endpoint'
import { useSettings } from '@/composables/useSettings'
import { useFeedback } from '@/composables/useFeedback'
import '@/styles/pages/settings.css'
import type { BackupData, ProxyStatusPayload, RestoreMode, ServerConfig, WebDAVBackupItem, WebDAVConfig } from '@/types/endpoint'

const BACKUP_DIR = '/clisimplehub'

const { t } = useI18n()
const feedback = useFeedback()
const {
  settingsForm,
  cliDirs,
  webdavForm,
  languageOptions,
  debugModeOptions,
  disableImageGenOptions,
  currentLanguage,
  getWebDAVConfigFromForm,
  loadGeneralData,
  loadWebDAVData,
  onLanguageSelect: onLanguageSelectCore,
  saveGeneralSettings: saveGeneralSettingsCore,
  testWebDAVConnection: testWebDAVConnectionCore,
  saveWebDAVSettings
} = useSettings()

const loading = ref(false)
const savingGeneral = ref(false)
const testingDatabase = ref(false)
const applyingDatabase = ref(false)
const testingWebdav = ref(false)
const savingWebdav = ref(false)
const loadingBackups = ref(false)
const backingUp = ref(false)
const restoringFilename = ref<string>('')
const deletingFilename = ref<string>('')
const loadingServers = ref(false)
const savingServer = ref(false)
const testingServer = ref(false)
const syncingServer = ref(false)
const copyingServerCurl = ref(false)
const pickingClashPath = ref(false)
const generalAutoSaveReady = ref(false)
const lastSavedGeneralSnapshot = ref('')
let generalSaveTimer: ReturnType<typeof setTimeout> | null = null
let offProxyStatusEvent: (() => void) | null = null

const backups = ref<WebDAVBackupItem[]>([])
const servers = ref<ServerConfig[]>([])
const retryingProxyStart = ref(false)
const proxyStatus = ref<ProxyStatusPayload>({
  running: false,
  port: 0,
  listenAddr: '',
  lastError: ''
})
const selectedServerIndex = ref<number>(-1)
const serverFormVisible = ref(false)
const editingServerIndex = ref<number>(-1)
const serverForm = ref<ServerConfig>({
  name: '',
  url: '',
  apiKey: ''
})

const serverOptions = computed<Array<{ label: string; value: number }>>(() =>
  servers.value.map((serverItem, index) => ({
    label: serverItem.name || serverItem.url,
    value: index
  }))
)
const hasSelectedServer = computed(() => selectedServerIndex.value >= 0 && selectedServerIndex.value < servers.value.length)
const selectedServer = computed(() => {
  if (!hasSelectedServer.value) return null
  return servers.value[selectedServerIndex.value]
})
const serverFormTitle = computed(() =>
  editingServerIndex.value >= 0 ? t('serverSync.edit') : t('serverSync.add')
)
const proxyStartupError = computed(() => String(proxyStatus.value.lastError || '').trim())

function toErrorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return String(error)
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

async function copyText(text: string): Promise<void> {
  try {
    if (typeof navigator !== 'undefined' && navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
      return
    }
  } catch {
    // fallback below
  }

  if (!fallbackCopyWithExecCommand(text)) {
    throw new Error('clipboard_unavailable')
  }
}

function parseWebDAVBackupsXml(xmlText: string): WebDAVBackupItem[] {
  const parser = new DOMParser()
  const xml = parser.parseFromString(xmlText, 'text/xml')
  const responses = xml.querySelectorAll('response')

  const parsedBackups: WebDAVBackupItem[] = []
  responses.forEach((resp, index) => {
    if (index === 0) return

    const href = resp.querySelector('href')?.textContent || ''
    const displayName = resp.querySelector('displayname')?.textContent || ''
    const lastModified = resp.querySelector('getlastmodified')?.textContent || ''

    let filename = ''
    if (href) {
      filename = decodeURIComponent(href.split('/').filter(Boolean).pop() || '')
    }
    if (!filename && displayName) {
      filename = displayName
    }
    if (!filename.endsWith('.json')) return

    parsedBackups.push({
      filename,
      displayName: displayName || filename,
      href: href || undefined,
      lastModified: lastModified || undefined,
      name: filename.replace('.json', '')
    })
  })

  return parsedBackups.sort((left, right) => {
    const leftTime = Date.parse(left.lastModified || '') || 0
    const rightTime = Date.parse(right.lastModified || '') || 0
    return rightTime - leftTime
  })
}

async function loadBackupsList(): Promise<void> {
  const config = getWebDAVConfigFromForm()

  if (!config.serverUrl) {
    backups.value = []
    return
  }

  loadingBackups.value = true
  try {
    const result = await endpointApi.webdavList({
      config,
      path: BACKUP_DIR,
      depth: '1'
    })

    if (result.error || (result.statusCode !== 200 && result.statusCode !== 207) || !result.body) {
      backups.value = []
      return
    }

    backups.value = parseWebDAVBackupsXml(result.body)
  } catch (error) {
    backups.value = []
    feedback.error(t('webdav.loadListFailed') + ': ' + toErrorMessage(error))
  } finally {
    loadingBackups.value = false
  }
}

async function ensureBackupDir(config: WebDAVConfig): Promise<boolean> {
  const result = await endpointApi.webdavMkcol({
    config,
    path: BACKUP_DIR
  })
  return result.statusCode === 200 || result.statusCode === 201 || result.statusCode === 405
}

async function loadServers(): Promise<void> {
  loadingServers.value = true
  try {
    servers.value = (await endpointApi.getServers()) || []

    if (servers.value.length === 0) {
      selectedServerIndex.value = -1
      return
    }

    if (selectedServerIndex.value < 0 || selectedServerIndex.value >= servers.value.length) {
      selectedServerIndex.value = 0
    }
  } catch (error) {
    servers.value = []
    selectedServerIndex.value = -1
    feedback.error(t('serverSync.loadFailed') + ': ' + toErrorMessage(error))
  } finally {
    loadingServers.value = false
  }
}

async function loadPageData(): Promise<void> {
  generalAutoSaveReady.value = false
  loading.value = true
  try {
    await Promise.all([
      loadGeneralData(),
      loadWebDAVData(),
      loadServers()
    ])
    await loadBackupsList()
    updateGeneralSnapshot()
    generalAutoSaveReady.value = true
  } catch (error) {
    feedback.error(t('settings.loadFailed') + ': ' + toErrorMessage(error))
  } finally {
    loading.value = false
  }
}

async function refreshProxyStatus(): Promise<void> {
  try {
    proxyStatus.value = await endpointApi.getProxyStatus()
  } catch (error) {
    console.error('Failed to load proxy status:', error)
  }
}

function formatBackupTime(raw?: string): string {
  if (!raw) return '-'
  const date = new Date(raw)
  if (Number.isFinite(date.getTime())) {
    return date.toLocaleString()
  }
  return raw
}

function getGeneralSnapshot(): string {
  return JSON.stringify({
    port: Number(settingsForm.value.port),
    apiKey: String(settingsForm.value.apiKey || ''),
    proxyUrl: String(settingsForm.value.proxyUrl || '').trim(),
    clashPath: String(settingsForm.value.clashPath || '').trim(),
    fallback: !!settingsForm.value.fallback,
    debugMode: String(settingsForm.value.debugMode || ''),
    listenAddr: String(settingsForm.value.listenAddr || ''),
    disableImageGeneration: String(settingsForm.value.disableImageGeneration || 'off'),
    claudeConfigDir: String(cliDirs.value.claudeConfigDir || '').trim(),
    codexConfigDir: String(cliDirs.value.codexConfigDir || '').trim()
  })
}

function updateGeneralSnapshot(): void {
  lastSavedGeneralSnapshot.value = getGeneralSnapshot()
}

function clearGeneralSaveTimer(): void {
  if (generalSaveTimer === null) return
  clearTimeout(generalSaveTimer)
  generalSaveTimer = null
}

function scheduleGeneralAutoSave(): void {
  if (!generalAutoSaveReady.value || loading.value) return
  clearGeneralSaveTimer()
  generalSaveTimer = setTimeout(() => {
    generalSaveTimer = null
    void saveGeneralSettings(true)
  }, 500)
}

async function saveGeneralSettings(quiet = false): Promise<void> {
  const snapshotBeforeSave = getGeneralSnapshot()
  if (quiet && snapshotBeforeSave === lastSavedGeneralSnapshot.value) {
    return
  }

  savingGeneral.value = true
  try {
    const saved = await saveGeneralSettingsCore({ quiet })
    await refreshProxyStatus()
    if (saved) {
      updateGeneralSnapshot()
    }
  } finally {
    savingGeneral.value = false
    const latestSnapshot = getGeneralSnapshot()
    if (
      generalAutoSaveReady.value &&
      latestSnapshot !== lastSavedGeneralSnapshot.value &&
      latestSnapshot !== snapshotBeforeSave
    ) {
      scheduleGeneralAutoSave()
    }
  }
}

async function retryProxyStart(): Promise<void> {
  if (retryingProxyStart.value) return

  retryingProxyStart.value = true
  try {
    await endpointApi.startProxy()
    await refreshProxyStatus()
  } catch (error) {
    feedback.error(t('settings.retryProxyStartFailed') + ': ' + toErrorMessage(error))
    await refreshProxyStatus()
  } finally {
    retryingProxyStart.value = false
  }
}

async function onLanguageSelect(lang: string | number | boolean | null): Promise<void> {
  await onLanguageSelectCore(lang)
}

async function pickClashExecutable(): Promise<void> {
  if (pickingClashPath.value) return

  pickingClashPath.value = true
  try {
    const selectedPath = await endpointApi.pickClashExecutable()
    if (!selectedPath) return
    settingsForm.value.clashPath = selectedPath
  } catch (error) {
    feedback.error(t('settings.saveFailed') + ': ' + toErrorMessage(error))
  } finally {
    pickingClashPath.value = false
  }
}

async function testDatabaseConnection(): Promise<void> {
  if (testingDatabase.value) return

  testingDatabase.value = true
  try {
    const result = await endpointApi.testDatabaseConnection(String(settingsForm.value.dbSource || '').trim())
    feedback.success(
      result.dbSource
        ? `${t('settings.databaseTestSuccess')}: ${result.dbSource}`
        : t('settings.databaseTestSuccess')
    )
  } catch (error) {
    feedback.error(t('settings.databaseTestFailed') + ': ' + toErrorMessage(error))
  } finally {
    testingDatabase.value = false
  }
}

async function applyDatabaseConfig(): Promise<void> {
  if (applyingDatabase.value) return

  applyingDatabase.value = true
  try {
    const result = await endpointApi.applyDatabaseConfig(String(settingsForm.value.dbSource || '').trim())
    await loadGeneralData()
    feedback.success(
      result.dbSource
        ? `${t('settings.databaseApplySuccess')}: ${result.dbSource}`
        : t('settings.databaseApplySuccess')
    )
  } catch (error) {
    feedback.error(t('settings.databaseApplyFailed') + ': ' + toErrorMessage(error))
  } finally {
    applyingDatabase.value = false
  }
}

async function testWebDAVConnection(): Promise<void> {
  testingWebdav.value = true
  try {
    await testWebDAVConnectionCore()
  } finally {
    testingWebdav.value = false
  }
}

async function saveWebDAVConfig(): Promise<void> {
  savingWebdav.value = true
  try {
    const saved = await saveWebDAVSettings()
    if (saved) {
      await loadBackupsList()
    }
  } finally {
    savingWebdav.value = false
  }
}

watch(
  () => [
    settingsForm.value.port,
    settingsForm.value.apiKey,
    settingsForm.value.proxyUrl,
    settingsForm.value.clashPath,
    settingsForm.value.fallback,
    settingsForm.value.debugMode,
    settingsForm.value.listenAddr,
    settingsForm.value.disableImageGeneration,
    cliDirs.value.claudeConfigDir,
    cliDirs.value.codexConfigDir
  ],
  () => {
    scheduleGeneralAutoSave()
  }
)

onBeforeUnmount(() => {
  clearGeneralSaveTimer()
  offProxyStatusEvent?.()
  offProxyStatusEvent = null
})

async function backupToWebDAV(): Promise<void> {
  const config = getWebDAVConfigFromForm()
  if (!config.serverUrl) {
    feedback.error(t('webdav.serverUrlRequired'))
    return
  }

  backingUp.value = true
  try {
    const dirReady = await ensureBackupDir(config)
    if (!dirReady) {
      throw new Error('failed_to_create_backup_dir')
    }

    const backupResponse = await endpointApi.createBackupData()
    if (!backupResponse.filename || !backupResponse.data) {
      throw new Error('invalid_backup_response')
    }

    const uploadResult = await endpointApi.webdavPut({
      config,
      path: `${BACKUP_DIR}/${backupResponse.filename}`,
      body: JSON.stringify(backupResponse.data, null, 2)
    })

    if (uploadResult.error) {
      throw new Error(uploadResult.error)
    }

    if (uploadResult.statusCode < 200 || uploadResult.statusCode >= 300) {
      throw new Error(String(uploadResult.statusCode))
    }

    await endpointApi.saveWebDAVConfig(config)
    feedback.success(t('webdav.backupSuccess').replace('{name}', backupResponse.filename))
    await loadBackupsList()
  } catch (error) {
    feedback.error(t('webdav.backupFailed') + ': ' + toErrorMessage(error))
  } finally {
    backingUp.value = false
  }
}

async function restoreBackup(backup: WebDAVBackupItem): Promise<void> {
  const config = getWebDAVConfigFromForm()
  if (!config.serverUrl) {
    feedback.error(t('webdav.serverUrlRequired'))
    return
  }

  const mode = await feedback.confirmWithOptions(t('webdav.restoreModePrompt'), {
    title: t('webdav.restoreModeTitle'),
    buttons: [
      { value: 'cancel', text: t('common.cancel') },
      { value: 'merge', text: t('webdav.restoreMerge'), primary: true },
      { value: 'replace', text: t('webdav.restoreReplace'), danger: true }
    ]
  })

  if (mode !== 'merge' && mode !== 'replace') {
    return
  }

  restoringFilename.value = backup.filename
  try {
    const result = await endpointApi.webdavGet({
      config,
      path: `${BACKUP_DIR}/${backup.filename}`
    })

    if (result.error) {
      throw new Error(result.error)
    }

    if (result.statusCode !== 200 || !result.body) {
      throw new Error(String(result.statusCode))
    }

    const backupData = JSON.parse(result.body) as BackupData
    await endpointApi.restoreBackupData(backupData, mode as RestoreMode)

    if (mode === 'replace') {
      feedback.success(t('webdav.restoreSuccessReplace'))
    } else {
      feedback.success(t('webdav.restoreSuccessMerge'))
    }

    await new Promise((resolve) => window.setTimeout(resolve, 400))
    try {
      await endpointApi.reloadConfig()
    } catch {
      // noop
    }
    window.dispatchEvent(new Event('home:endpoints-updated'))
    window.dispatchEvent(new Event('home:logs-updated'))
    await loadPageData()
  } catch (error) {
    feedback.error(t('webdav.restoreFailed') + ': ' + toErrorMessage(error))
  } finally {
    restoringFilename.value = ''
  }
}

async function deleteBackup(backup: WebDAVBackupItem): Promise<void> {
  const config = getWebDAVConfigFromForm()
  if (!config.serverUrl) {
    feedback.error(t('webdav.serverUrlRequired'))
    return
  }

  const confirmed = await feedback.confirm(
    t('webdav.deleteBackupConfirm').replace('{name}', backup.filename),
    {
      title: t('webdav.deleteBackupTitle'),
      confirmText: t('common.delete'),
      cancelText: t('common.cancel'),
      danger: true
    }
  )

  if (!confirmed) return

  deletingFilename.value = backup.filename
  try {
    const result = await endpointApi.webdavDelete({
      config,
      path: `${BACKUP_DIR}/${backup.filename}`
    })

    if (result.error) {
      throw new Error(result.error)
    }

    if (result.statusCode < 200 || result.statusCode >= 300) {
      throw new Error(String(result.statusCode))
    }

    feedback.success(t('webdav.deleteBackupSuccess'))
    await loadBackupsList()
  } catch (error) {
    feedback.error(t('webdav.deleteBackupFailed') + ': ' + toErrorMessage(error))
  } finally {
    deletingFilename.value = ''
  }
}

function startAddServer(): void {
  editingServerIndex.value = -1
  serverFormVisible.value = true
  serverForm.value = {
    name: '',
    url: '',
    apiKey: ''
  }
}

function startEditServer(): void {
  if (!selectedServer.value) return

  editingServerIndex.value = selectedServerIndex.value
  serverFormVisible.value = true
  serverForm.value = {
    name: selectedServer.value.name || '',
    url: selectedServer.value.url,
    apiKey: selectedServer.value.apiKey || ''
  }
}

function cancelServerForm(): void {
  serverFormVisible.value = false
  editingServerIndex.value = -1
}

function handleServerFormVisibleChange(show: boolean): void {
  serverFormVisible.value = show
  if (!show) {
    editingServerIndex.value = -1
  }
}

async function saveServer(): Promise<void> {
  const normalizedUrl = serverForm.value.url.trim()
  if (!normalizedUrl) {
    feedback.error(t('serverSync.urlRequired'))
    return
  }

  const entry: ServerConfig = {
    name: (serverForm.value.name || '').trim(),
    url: normalizedUrl,
    apiKey: (serverForm.value.apiKey || '').trim()
  }

  savingServer.value = true
  try {
    const nextServers = [...servers.value]
    const targetIndex =
      editingServerIndex.value >= 0 && editingServerIndex.value < nextServers.length
        ? editingServerIndex.value
        : nextServers.length

    if (editingServerIndex.value >= 0 && editingServerIndex.value < nextServers.length) {
      nextServers[editingServerIndex.value] = entry
    } else {
      nextServers.push(entry)
    }

    await endpointApi.saveServers(nextServers)
    await loadServers()

    selectedServerIndex.value = Math.min(targetIndex, Math.max(servers.value.length - 1, 0))
    cancelServerForm()
    feedback.success(t('serverSync.saveSuccess'))
  } catch (error) {
    feedback.error(t('serverSync.saveFailed') + toErrorMessage(error))
  } finally {
    savingServer.value = false
  }
}

async function deleteSelectedServer(): Promise<void> {
  if (!selectedServer.value) return

  const confirmed = await feedback.confirm(
    `${t('serverSync.deleteConfirm')} "${selectedServer.value.name || selectedServer.value.url}"?`,
    {
      title: t('serverSync.delete'),
      confirmText: t('common.delete'),
      cancelText: t('common.cancel'),
      danger: true
    }
  )

  if (!confirmed) return

  try {
    const nextServers = servers.value.filter((_, index) => index !== selectedServerIndex.value)
    await endpointApi.saveServers(nextServers)
    await loadServers()
    cancelServerForm()
    feedback.success(t('serverSync.deleteSuccess'))
  } catch (error) {
    feedback.error(t('serverSync.deleteFailed') + toErrorMessage(error))
  }
}

async function testSelectedServer(): Promise<void> {
  if (!selectedServer.value) return

  testingServer.value = true
  try {
    await endpointApi.testServerConnection(selectedServer.value.url, selectedServer.value.apiKey || '')
    feedback.success(t('serverSync.testSuccess'))
  } catch (error) {
    feedback.error(t('serverSync.testFailed') + toErrorMessage(error))
  } finally {
    testingServer.value = false
  }
}

async function syncSelectedServer(): Promise<void> {
  if (!selectedServer.value) return

  const confirmed = await feedback.confirm(
    `${t('serverSync.syncConfirm')} "${selectedServer.value.name || selectedServer.value.url}"?`,
    {
      title: t('serverSync.title'),
      confirmText: t('serverSync.sync'),
      cancelText: t('common.cancel')
    }
  )

  if (!confirmed) return

  syncingServer.value = true
  try {
    await endpointApi.syncConfigToServer(selectedServerIndex.value)
    feedback.success(t('serverSync.syncSuccess'))
  } catch (error) {
    feedback.error(t('serverSync.syncFailed') + toErrorMessage(error))
  } finally {
    syncingServer.value = false
  }
}

async function copySelectedServerCurl(): Promise<void> {
  if (!selectedServer.value) return

  copyingServerCurl.value = true
  try {
    const curlCommand = await endpointApi.buildSyncConfigCurl(selectedServerIndex.value)
    await copyText(curlCommand)
    feedback.success(t('serverSync.copyCurlSuccess'))
  } catch (error) {
    feedback.error(t('serverSync.copyCurlFailed') + toErrorMessage(error))
  } finally {
    copyingServerCurl.value = false
  }
}

onMounted(() => {
  void loadPageData()
  void refreshProxyStatus()

  try {
    const runtime = (window as Window & { runtime?: { EventsOn?: (event: string, callback: () => void) => (() => void) | void } }).runtime
    if (runtime?.EventsOn) {
      const off = runtime.EventsOn('proxy:status-changed', () => {
        void refreshProxyStatus()
      })
      if (typeof off === 'function') {
        offProxyStatusEvent = off
      }
    }
  } catch {
    // ignore
  }
})
</script>

<template>
  <div class="settings-page">
    <div class="settings-scroll">
      <section class="settings-card">
        <div class="settings-card-header">
          <h2>{{ t('settings.sectionGeneral') }}</h2>
          <div class="settings-card-header-actions">
            <span class="settings-inline-hint">
              {{ savingGeneral ? t('settings.autoSaving') : t('settings.autoSaveHint') }}
            </span>
          </div>
        </div>

        <div v-if="loading" class="settings-loading">{{ t('common.loading') }}</div>
        <div v-else class="kv-grid">
          <label>{{ t('header.language') }}</label>
          <n-radio-group
            :value="currentLanguage"
            @update:value="onLanguageSelect"
          >
            <n-radio-button
              v-for="item in languageOptions"
              :key="item.value"
              :value="item.value"
            >
              {{ item.label }}
            </n-radio-button>
          </n-radio-group>

          <label>{{ t('settings.port') }}</label>
          <div>
            <n-alert
              v-if="proxyStartupError"
              type="error"
              :title="t('settings.proxyStartFailedTitle')"
              style="margin-bottom: 12px"
            >
              <div>{{ t('settings.proxyStartFailedHelp') }}</div>
              <div style="margin-top: 6px; word-break: break-word;">
                {{ proxyStartupError }}
              </div>
              <template #action>
                <n-button size="small" :loading="retryingProxyStart" @click="retryProxyStart">
                  {{ t('common.retry') }}
                </n-button>
              </template>
            </n-alert>
            <input v-model.number="settingsForm.port" type="number" min="1" max="65535" placeholder="5600">
            <small>{{ t('settings.portHelp') }}</small>
          </div>

          <label>{{ t('settings.listenAddr') }}</label>
          <div>
            <input
              v-model="settingsForm.listenAddr"
              type="text"
              :placeholder="t('settings.listenAddrPlaceholder')"
            >
            <small>{{ t('settings.listenAddrHelp') }}</small>
          </div>

	          <label>{{ t('settings.apiKey') }}</label>
	          <div>
	            <input v-model="settingsForm.apiKey" type="password" :placeholder="t('settings.apiKeyPlaceholder')">
	            <small>{{ t('settings.apiKeyHelp') }}</small>
	          </div>

          <label>{{ t('settings.proxyUrl') }}</label>
          <div>
            <input v-model="settingsForm.proxyUrl" type="text" :placeholder="t('settings.proxyUrlPlaceholder')">
            <small>{{ t('settings.proxyUrlHelp') }}</small>
          </div>

          <label>{{ t('settings.clashPath') }}</label>
          <div>
            <n-input-group>
              <n-input
                v-model:value="settingsForm.clashPath"
                :placeholder="t('settings.clashPathPlaceholder')"
              />
              <n-button
                :disabled="pickingClashPath"
                @click="pickClashExecutable"
              >
                ...
              </n-button>
            </n-input-group>
            <small>{{ t('settings.clashPathHelp') }}</small>
          </div>

          <label>{{ t('settings.databaseDsn') }}</label>
          <div>
            <n-input-group>
              <n-input
                v-model:value="settingsForm.dbSource"
                :placeholder="t('settings.databaseDsnPlaceholder')"
              />
              <n-button
                :loading="testingDatabase"
                @click="testDatabaseConnection"
              >
                {{ t('settings.databaseTest') }}
              </n-button>
              <n-button
                type="primary"
                :loading="applyingDatabase"
                @click="applyDatabaseConfig"
              >
                {{ t('settings.databaseApply') }}
              </n-button>
            </n-input-group>
            <small>{{ t('settings.databaseDsnHelp') }}</small>
          </div>

          <label>{{ t('settings.fallback') }}</label>
          <div>
            <label class="inline-flex items-center">
              <span class="relative inline-flex h-6 w-11 shrink-0 items-center">
                <input v-model="settingsForm.fallback" type="checkbox" class="peer sr-only">
                <span
                  class="h-6 w-11 rounded-full bg-slate-300 transition-colors duration-200 peer-checked:bg-sky-600"
                ></span>
                <span
                  class="pointer-events-none absolute left-0.5 top-0.5 h-5 w-5 rounded-full bg-white shadow transition-transform duration-200 peer-checked:translate-x-5"
                ></span>
              </span>
            </label>
            <small>{{ t('settings.fallbackHelp') }}</small>
          </div>

          <label>{{ t('settings.debugMode') }}</label>
          <div>
            <n-select
              v-model:value="settingsForm.debugMode"
              class="form-control"
              :options="debugModeOptions"
            />
            <small>{{ t('settings.debugModeHelp') }}</small>
          </div>

          <label>{{ t('settings.disableImageGeneration') }}</label>
          <div>
            <n-select
              v-model:value="settingsForm.disableImageGeneration"
              class="form-control"
              :options="disableImageGenOptions"
            />
            <small>{{ t('settings.disableImageGenerationHelp') }}</small>
          </div>

          <label>{{ t('settings.claudeConfigDir') }}</label>
          <div>
            <input v-model="cliDirs.claudeConfigDir" type="text" placeholder="~/.claude">
            <small>{{ t('settings.claudeConfigDirHelp') }}</small>
          </div>

          <label>{{ t('settings.codexConfigDir') }}</label>
          <div>
            <input v-model="cliDirs.codexConfigDir" type="text" placeholder="~/.codex">
            <small>{{ t('settings.codexConfigDirHelp') }}</small>
          </div>
        </div>
      </section>

      <section class="settings-card">
        <div class="settings-card-header">
          <h2>{{ t('webdav.sectionTitle') }}</h2>
        </div>

        <div class="kv-grid">
          <label>{{ t('webdav.serverUrl') }}</label>
          <div>
            <div class="field-with-action">
              <input v-model="webdavForm.serverUrl" type="text" :placeholder="t('webdav.serverUrlPlaceholder')">
              <button class="btn btn-sm btn-secondary" :disabled="testingWebdav" @click="testWebDAVConnection">
                {{ testingWebdav ? t('webdav.testing') : t('webdav.test') }}
              </button>
            </div>
          </div>

          <label>{{ t('webdav.username') }}</label>
          <input v-model="webdavForm.username" type="text" :placeholder="t('webdav.usernamePlaceholder')">

          <label>{{ t('webdav.password') }}</label>
          <n-input
            v-model:value="webdavForm.password"
            type="password"
            show-password-on="click"
            :placeholder="t('webdav.passwordPlaceholder')"
          />
        </div>

        <div class="settings-card-actions">
          <button class="btn btn-primary" :disabled="savingWebdav" @click="saveWebDAVConfig">
            {{ savingWebdav ? t('settings.saving') : t('webdav.save') }}
          </button>
        </div>
      </section>

      <section class="settings-card">
        <div class="settings-card-header">
          <h2>{{ t('webdav.backupsTitle') }}</h2>
          <div class="settings-card-header-actions">
            <button class="btn btn-sm btn-secondary" :disabled="loadingBackups" @click="loadBackupsList">
              {{ t('webdav.refreshList') }}
            </button>
            <button class="btn btn-sm btn-primary" :disabled="backingUp" @click="backupToWebDAV">
              {{ backingUp ? t('webdav.backingUp') : t('webdav.backupNow') }}
            </button>
          </div>
        </div>

        <div v-if="loadingBackups" class="settings-loading">{{ t('common.loading') }}</div>
        <div v-else-if="backups.length === 0" class="empty-state">{{ t('webdav.noBackups') }}</div>
        <div v-else class="backup-list">
          <div v-for="backup in backups" :key="backup.filename" class="backup-item">
            <div class="backup-meta">
              <div class="backup-name">{{ backup.displayName }}</div>
              <div class="backup-time">{{ formatBackupTime(backup.lastModified) }}</div>
            </div>
            <div class="backup-actions">
              <button
                class="btn btn-sm btn-secondary"
                :disabled="restoringFilename === backup.filename"
                @click="restoreBackup(backup)"
              >
                {{ restoringFilename === backup.filename ? t('webdav.restoring') : t('webdav.restore') }}
              </button>
              <button
                class="btn btn-sm btn-danger"
                :disabled="deletingFilename === backup.filename"
                @click="deleteBackup(backup)"
              >
                {{ deletingFilename === backup.filename ? t('webdav.deleting') : t('common.delete') }}
              </button>
            </div>
          </div>
        </div>
      </section>

      <section class="settings-card">
        <div class="settings-card-header">
          <h2>{{ t('serverSync.title') }}</h2>
        </div>

        <div class="server-toolbar">
          <n-select
            v-model:value="selectedServerIndex"
            class="form-control"
            :options="serverOptions"
            :disabled="loadingServers || serverOptions.length === 0"
            :placeholder="serverOptions.length === 0 ? t('serverSync.noServers') : undefined"
          />

          <button class="btn btn-sm btn-secondary" @click="startAddServer">{{ t('serverSync.add') }}</button>
          <button class="btn btn-sm btn-secondary" :disabled="!hasSelectedServer" @click="startEditServer">{{ t('serverSync.edit') }}</button>
          <button class="btn btn-sm btn-secondary" :disabled="!hasSelectedServer || testingServer" @click="testSelectedServer">
            {{ testingServer ? t('serverSync.testing') : t('serverSync.test') }}
          </button>
          <button class="btn btn-sm btn-danger" :disabled="!hasSelectedServer" @click="deleteSelectedServer">{{ t('serverSync.delete') }}</button>
        </div>

        <div class="settings-card-actions">
          <button class="btn btn-secondary" :disabled="!hasSelectedServer || copyingServerCurl" @click="copySelectedServerCurl">
            {{ copyingServerCurl ? t('serverSync.copyingCurl') : t('serverSync.copyCurl') }}
          </button>
          <button class="btn btn-primary" :disabled="!hasSelectedServer || syncingServer" @click="syncSelectedServer">
            {{ syncingServer ? t('serverSync.syncing') : t('serverSync.sync') }}
          </button>
        </div>
      </section>
    </div>

    <n-modal
      :show="serverFormVisible"
      preset="card"
      class="settings-server-modal"
      :title="`${t('serverSync.title')} - ${serverFormTitle}`"
      :style="{ width: 'min(620px, 94vw)' }"
      :mask-closable="!savingServer"
      :close-on-esc="!savingServer"
      @update:show="handleServerFormVisibleChange"
    >
      <div class="settings-server-modal-grid">
        <label>{{ t('serverSync.name') }}</label>
        <n-input v-model:value="serverForm.name" :placeholder="t('serverSync.namePlaceholder')" />

        <label>{{ t('serverSync.url') }}</label>
        <n-input v-model:value="serverForm.url" :placeholder="t('serverSync.urlPlaceholder')" />

        <label>{{ t('serverSync.apiKey') }}</label>
        <n-input
          v-model:value="serverForm.apiKey"
          type="password"
          show-password-on="click"
          :placeholder="t('serverSync.apiKeyPlaceholder')"
        />
      </div>

      <template #footer>
        <div class="settings-server-modal-actions">
          <n-button :disabled="savingServer" @click="cancelServerForm">{{ t('common.cancel') }}</n-button>
          <n-button type="primary" :loading="savingServer" @click="saveServer">{{ t('common.save') }}</n-button>
        </div>
      </template>
    </n-modal>
  </div>
</template>
