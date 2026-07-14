<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NButton,
  NCard,
  NInput,
  NModal,
  NSpace,
  NSpin,
  NTabPane,
  NTabs,
  NTag,
  NText
} from 'naive-ui'
import { endpointApi } from '@/api/endpoint'
import { useFeedback } from '@/composables/useFeedback'
import type { CLIConfigFile } from '@/types/endpoint'

const { t } = useI18n()
const feedback = useFeedback()

const show = ref(false)
const loading = ref(false)
const saving = ref(false)
const files = ref<CLIConfigFile[]>([])
const activeTab = ref('config.toml')

const configFile = computed(() => files.value.find((f) => f.name === 'config.toml'))
const authFile = computed(() => files.value.find((f) => f.name === 'auth.json'))

function toErrorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return String(error)
}

function resetState(): void {
  loading.value = false
  saving.value = false
  files.value = []
  activeTab.value = 'config.toml'
}

function handleModalVisibleChange(next: boolean): void {
  show.value = next
  if (!next) {
    resetState()
  }
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

function ensureValidJson(content: string, fileName: string): void {
  try {
    JSON.parse(content)
  } catch {
    throw new Error(`${t('cliConfig.invalidJson')}: ${fileName}`)
  }
}

async function open(): Promise<void> {
  show.value = true
  loading.value = true
  files.value = []
  activeTab.value = 'config.toml'

  try {
    const result = await endpointApi.getGrokConfig()
    if (!result.success) {
      throw new Error(result.message || 'load config failed')
    }
    files.value = (result.files || []).map((file) => ({ ...file }))
    if (files.value.length > 0) {
      activeTab.value = files.value[0].name
    }
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

async function handleSave(): Promise<void> {
  saving.value = true
  try {
    const configToml = getRequiredFileContent('config.toml')
    const authJson = getRequiredFileContent('auth.json')
    if (!configToml.trim()) {
      throw new Error('config.toml cannot be empty')
    }
    ensureValidJson(authJson, 'auth.json')
    await endpointApi.saveGrokConfig(configToml, authJson)
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
    :mask-closable="!(loading || saving)"
    @update:show="handleModalVisibleChange"
  >
    <n-card
      class="grok-config-modal-card"
      :title="`Grok ${t('cliConfig.title')}`"
      :bordered="false"
      :closable="!(loading || saving)"
      size="small"
      role="dialog"
      aria-modal="true"
      @close="close"
    >
      <template #header-extra>
        <n-tag size="small" type="info">~/.grok</n-tag>
      </template>

      <div v-if="loading" class="grok-config-loading">
        <n-spin size="small" />
        <n-text>{{ t('common.loading') }}</n-text>
      </div>

      <div v-else class="grok-config-content">
        <n-tabs v-model:value="activeTab" type="line" size="small" animated>
          <n-tab-pane name="config.toml" tab="config.toml" display-directive="show">
            <div class="grok-config-file">
              <div class="grok-config-file-header">
                <n-text strong>config.toml</n-text>
                <n-tag size="small" :type="configFile?.exists ? 'success' : 'warning'">
                  {{ configFile?.exists ? t('cliConfig.fileExists') : t('cliConfig.fileNew') }}
                </n-tag>
              </div>
              <n-input
                :value="configFile?.content || ''"
                class="grok-config-textarea"
                type="textarea"
                :autosize="false"
                :spellcheck="false"
                @update:value="(v) => updateFileContent('config.toml', v)"
              />
            </div>
          </n-tab-pane>

          <n-tab-pane name="auth.json" tab="auth.json" display-directive="show">
            <div class="grok-config-file">
              <div class="grok-config-file-header">
                <n-text strong>auth.json</n-text>
                <n-tag size="small" :type="authFile?.exists ? 'success' : 'warning'">
                  {{ authFile?.exists ? t('cliConfig.fileExists') : t('cliConfig.fileNew') }}
                </n-tag>
              </div>
              <n-input
                :value="authFile?.content || ''"
                class="grok-config-textarea"
                type="textarea"
                :autosize="false"
                :spellcheck="false"
                @update:value="(v) => updateFileContent('auth.json', v)"
              />
            </div>
          </n-tab-pane>
        </n-tabs>
      </div>

      <template #footer>
        <n-space justify="end">
          <n-button :disabled="loading || saving" @click="close">
            {{ t('common.cancel') }}
          </n-button>
          <n-button type="primary" :loading="saving" :disabled="loading" @click="handleSave">
            {{ t('cliConfig.save') }}
          </n-button>
        </n-space>
      </template>
    </n-card>
  </n-modal>
</template>

<style scoped>
.grok-config-modal-card {
  width: min(960px, calc(100vw - 40px));
  height: min(860px, calc(100vh - 40px));
  max-height: calc(100vh - 40px);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.grok-config-modal-card :deep(.n-card__content) {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.grok-config-modal-card :deep(.n-card__footer) {
  flex: 0 0 auto;
}

.grok-config-loading {
  flex: 1;
  min-height: 220px;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
}

.grok-config-content {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.grok-config-content :deep(.n-tabs) {
  height: 100%;
  display: flex;
  flex-direction: column;
}

.grok-config-content :deep(.n-tabs-nav) {
  flex: 0 0 auto;
}

.grok-config-content :deep(.n-tabs-pane-wrapper) {
  flex: 1 1 auto;
  min-height: 0;
  overflow: hidden;
}

.grok-config-content :deep(.n-tab-pane) {
  height: 100%;
}

.grok-config-file {
  height: 100%;
  min-height: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding-top: 4px;
  overflow: hidden;
}

.grok-config-file-header {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.grok-config-textarea {
  flex: 1 1 auto;
  min-height: 0;
  height: 100%;
  display: flex !important;
}

.grok-config-textarea :deep(.n-input-wrapper) {
  flex: 1 1 auto;
  height: 100%;
  min-height: 0;
  align-items: stretch;
}

.grok-config-textarea :deep(textarea) {
  height: 100% !important;
  min-height: 0 !important;
  max-height: none !important;
  resize: none;
  overflow: auto !important;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New', monospace;
  font-size: 12.5px;
  line-height: 1.45;
}

@media (max-width: 900px) {
  .grok-config-modal-card {
    width: calc(100vw - 20px);
    height: calc(100vh - 20px);
    max-height: calc(100vh - 20px);
  }
}
</style>
