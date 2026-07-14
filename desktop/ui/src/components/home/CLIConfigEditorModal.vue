<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NButton,
  NCard,
  NInput,
  NModal,
  NSelect,
  NSpace,
  NSpin,
  NTabPane,
  NTabs,
  NTag,
  NText
} from 'naive-ui'
import { endpointApi } from '@/api/endpoint'
import { useFeedback } from '@/composables/useFeedback'
import type { CLIConfigFile, InterfaceType, LocalIPInfo } from '@/types/endpoint'

type EditorType = 'claude' | 'codex'

const { t } = useI18n()
const feedback = useFeedback()

const show = ref(false)
const loading = ref(false)
const processing = ref(false)
const saving = ref(false)
const currentEditorType = ref<EditorType | null>(null)
const files = ref<CLIConfigFile[]>([])
const activeTab = ref('')
const localIPs = ref<LocalIPInfo[]>([])
const selectedIP = ref<string | null>(null)
const proxyPort = ref(5600)

const ipOptions = computed<Array<{ label: string; value: string }>>(() =>
  localIPs.value.map((ip) => ({
    label: ip.interface === 'localhost' ? `${ip.ip} (${t('cliConfig.localhost')})` : `${ip.ip} (${ip.interface})`,
    value: ip.ip
  }))
)

const modalTitle = computed(() => {
  if (currentEditorType.value === 'claude') return `Claude Code ${t('cliConfig.title')}`
  if (currentEditorType.value === 'codex') return `Codex ${t('cliConfig.title')}`
  return t('cliConfig.title')
})

const currentProxyUrl = computed(() => {
  if (!currentEditorType.value || !selectedIP.value) return ''
  const base = `http://${selectedIP.value}:${proxyPort.value || 5600}`
  return currentEditorType.value === 'codex' ? `${base}/v1` : base
})

const proxyPreviewText = computed(() => {
  if (!currentProxyUrl.value) return ''
  return `Proxy: ${currentProxyUrl.value}`
})

function toErrorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return String(error)
}

function toEditorType(type: InterfaceType | EditorType): EditorType | null {
  if (type === 'claude' || type === 'codex') return type
  return null
}

function resolveSelectedIP(savedListenAddr: string | undefined, ips: LocalIPInfo[]): string | null {
  if (ips.length === 0) return null
  const exact = savedListenAddr ? ips.find((item) => item.ip === savedListenAddr) : undefined
  if (exact) return exact.ip
  return ips[0]?.ip || null
}

function resolveListenAddrFromIP(ip: string): string {
  if (ip === '127.0.0.1') return '127.0.0.1'
  if (ip === '::1') return '::1'
  return '0.0.0.0'
}

function resetState(): void {
  loading.value = false
  processing.value = false
  saving.value = false
  currentEditorType.value = null
  files.value = []
  activeTab.value = ''
  localIPs.value = []
  selectedIP.value = null
  proxyPort.value = 5600
}

function updateFileContent(fileName: string, content: string): void {
  const target = files.value.find((item) => item.name === fileName)
  if (!target) return
  target.content = content
}

function getRequiredFileContent(fileName: string): string {
  const target = files.value.find((item) => item.name === fileName)
  if (!target) {
    throw new Error(`${fileName} not found`)
  }
  return target.content || ''
}

function isProxyConfigured(file: CLIConfigFile): boolean {
  if (!currentProxyUrl.value) return !!file.isProxyConfigured
  return file.content.includes(currentProxyUrl.value)
}

function handleModalVisibleChange(next: boolean): void {
  show.value = next
  if (!next) {
    resetState()
  }
}

async function open(type: InterfaceType | EditorType): Promise<void> {
  const editorType = toEditorType(type)
  if (!editorType) return

  show.value = true
  loading.value = true
  currentEditorType.value = editorType
  files.value = []
  activeTab.value = ''

  try {
    const [ips, settings, result] = await Promise.all([
      endpointApi.getLocalIPs(),
      endpointApi.getSettings(),
      editorType === 'claude' ? endpointApi.getClaudeConfig() : endpointApi.getCodexConfig()
    ])

    if (!result.success) {
      throw new Error(result.message || 'load config failed')
    }

    localIPs.value = ips || []
    proxyPort.value = settings?.port || 5600
    selectedIP.value = resolveSelectedIP(settings?.listenAddr, localIPs.value)
    files.value = (result.files || []).map((file) => ({ ...file }))
    activeTab.value = files.value[0]?.name || ''
  } catch (error) {
    feedback.error(t('cliConfig.loadFailed') + ': ' + toErrorMessage(error))
    show.value = false
    resetState()
  } finally {
    loading.value = false
  }
}

function close(): void {
  show.value = false
  resetState()
}

async function handleProcess(): Promise<void> {
  if (!currentEditorType.value) return
  if (!selectedIP.value) {
    feedback.error(t('cliConfig.processFailed') + ': No IP selected')
    return
  }

  processing.value = true
  try {
    if (currentEditorType.value === 'claude') {
      const settingsJson = getRequiredFileContent('settings.json')
      const processed = await endpointApi.processClaudeConfigWithIP(settingsJson, selectedIP.value)
      updateFileContent('settings.json', processed)
    } else {
      const configToml = getRequiredFileContent('config.toml')
      const authJson = getRequiredFileContent('auth.json')
      const result = await endpointApi.processCodexConfigWithIP(configToml, authJson, selectedIP.value)
      updateFileContent('config.toml', result.configToml)
      updateFileContent('auth.json', result.authJson)
    }

    feedback.success(t('cliConfig.processSuccess'))
  } catch (error) {
    feedback.error(t('cliConfig.processFailed') + ': ' + toErrorMessage(error))
  } finally {
    processing.value = false
  }
}

function ensureValidJson(content: string, fileName: string): void {
  try {
    JSON.parse(content)
  } catch {
    throw new Error(`${t('cliConfig.invalidJson')}: ${fileName}`)
  }
}

async function handleSave(): Promise<void> {
  if (!currentEditorType.value) return
  saving.value = true

  try {
    if (currentEditorType.value === 'claude') {
      const settingsJson = getRequiredFileContent('settings.json')
      ensureValidJson(settingsJson, 'settings.json')
      await endpointApi.saveClaudeConfig(settingsJson)
    } else {
      const configToml = getRequiredFileContent('config.toml')
      const authJson = getRequiredFileContent('auth.json')
      ensureValidJson(authJson, 'auth.json')
      await endpointApi.saveCodexConfig(configToml, authJson)
    }

    if (selectedIP.value) {
      try {
        await endpointApi.saveListenAddr(resolveListenAddrFromIP(selectedIP.value))
      } catch (error) {
        console.error('Failed to save listen address:', error)
      }
    }

    feedback.success(t('cliConfig.saveSuccess'))
    close()
  } catch (error) {
    feedback.error(t('cliConfig.saveFailed') + ': ' + toErrorMessage(error))
  } finally {
    saving.value = false
  }
}

defineExpose({
  open,
  close
})
</script>

<template>
  <n-modal
    :show="show"
    :mask-closable="!(loading || saving || processing)"
    @update:show="handleModalVisibleChange"
  >
    <n-card
      class="cli-config-modal-card"
      :title="modalTitle"
      :bordered="false"
      :closable="!(loading || saving || processing)"
      size="small"
      role="dialog"
      aria-modal="true"
      @close="close"
    >
      <template #header-extra>
        <n-tag v-if="currentEditorType" size="small" :type="currentEditorType === 'claude' ? 'info' : 'warning'">
          {{ currentEditorType === 'claude' ? 'Claude Code' : 'Codex' }}
        </n-tag>
      </template>

      <div v-if="loading" class="cli-config-loading">
        <n-spin size="small" />
        <n-text>{{ t('common.loading') }}</n-text>
      </div>

      <div v-else class="cli-config-content">
        <div class="cli-config-ip-selector">
          <div class="cli-config-ip-label">{{ t('cliConfig.selectIP') }}</div>
          <n-select
            v-model:value="selectedIP"
            :options="ipOptions"
            :placeholder="t('cliConfig.selectIP')"
            filterable
          />
          <n-text depth="3" class="cli-config-ip-hint">{{ t('cliConfig.selectIPHint') }}</n-text>
        </div>

        <div class="cli-config-files">
          <n-tabs v-model:value="activeTab" type="line" size="small" animated>
            <n-tab-pane
              v-for="file in files"
              :key="file.name"
              :name="file.name"
              :tab="file.name"
              display-directive="show"
            >
              <div class="cli-config-file">
                <div class="cli-config-file-header">
                  <n-text strong>{{ file.name }}</n-text>
                  <n-tag size="small" :type="isProxyConfigured(file) ? 'success' : 'warning'">
                    {{ isProxyConfigured(file) ? `✅ ${t('cliConfig.proxyConfigured')}` : `❌ ${t('cliConfig.proxyNotConfigured')}` }}
                  </n-tag>
                </div>
                <n-input
                  :value="file.content"
                  class="cli-config-textarea"
                  type="textarea"
                  :autosize="false"
                  :spellcheck="false"
                  @update:value="(v) => updateFileContent(file.name, v)"
                />
              </div>
            </n-tab-pane>
          </n-tabs>
        </div>
      </div>

      <template #footer>
        <n-space justify="space-between" align="center" :wrap-item="false">
          <n-text depth="3" class="cli-config-proxy-preview">{{ proxyPreviewText }}</n-text>
          <n-space>
            <n-button :loading="processing" :disabled="loading || saving || !selectedIP" @click="handleProcess">
              {{ t('cliConfig.process') }}
            </n-button>
            <n-button type="primary" :loading="saving" :disabled="loading || processing" @click="handleSave">
              {{ t('cliConfig.save') }}
            </n-button>
          </n-space>
        </n-space>
      </template>
    </n-card>
  </n-modal>
</template>

<style scoped>
.cli-config-modal-card {
  width: min(960px, calc(100vw - 40px));
  height: min(860px, calc(100vh - 40px));
  max-height: calc(100vh - 40px);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.cli-config-modal-card :deep(.n-card__content) {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.cli-config-modal-card :deep(.n-card__footer) {
  flex: 0 0 auto;
}

.cli-config-loading {
  flex: 1;
  min-height: 220px;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
}

.cli-config-content {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  gap: 12px;
  overflow: hidden;
}

.cli-config-ip-selector {
  flex: 0 0 auto;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.cli-config-ip-label {
  font-weight: 600;
}

.cli-config-ip-hint {
  line-height: 1.35;
}

.cli-config-files {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.cli-config-files :deep(.n-tabs) {
  height: 100%;
  display: flex;
  flex-direction: column;
}

.cli-config-files :deep(.n-tabs-nav) {
  flex: 0 0 auto;
}

.cli-config-files :deep(.n-tabs-pane-wrapper) {
  flex: 1 1 auto;
  min-height: 0;
  overflow: hidden;
}

.cli-config-files :deep(.n-tab-pane) {
  height: 100%;
}

.cli-config-file {
  height: 100%;
  min-height: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding-top: 4px;
  overflow: hidden;
}

.cli-config-file-header {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.cli-config-textarea {
  flex: 1 1 auto;
  min-height: 0;
  height: 100%;
  display: flex !important;
}

.cli-config-textarea :deep(.n-input-wrapper) {
  flex: 1 1 auto;
  height: 100%;
  min-height: 0;
  align-items: stretch;
}

.cli-config-textarea :deep(textarea) {
  height: 100% !important;
  min-height: 0 !important;
  max-height: none !important;
  resize: none;
  overflow: auto !important;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New', monospace;
  font-size: 12.5px;
  line-height: 1.45;
}

.cli-config-proxy-preview {
  max-width: 420px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

@media (max-width: 900px) {
  .cli-config-modal-card {
    width: calc(100vw - 20px);
    height: calc(100vh - 20px);
    max-height: calc(100vh - 20px);
  }

  .cli-config-proxy-preview {
    display: none;
  }
}
</style>
