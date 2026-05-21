<template>
  <n-modal
    v-model:show="visible"
    preset="card"
    :title="t('codex.configModalTitle')"
    style="width: 760px"
  >
    <n-form label-placement="left" label-width="130" label-align="right" :model="form">
      <n-grid :cols="24" :x-gap="12">
        <n-form-item-gi :span="24" :label="`${t('codex.rotationModeLabel')}:`">
          <div class="config-field">
            <n-select v-model:value="form.rotationMode" :options="rotationModeOptions" />
            <n-text depth="3" class="config-help">{{ t('codex.rotationModeHelp2') }}</n-text>
          </div>
        </n-form-item-gi>

        <n-form-item-gi :span="24" :label="`${t('codex.pluginProxyUrl')}:`">
          <div class="config-field">
            <n-input v-model:value="form.proxyUrl" :placeholder="t('codex.pluginProxyUrlPlaceholder')" />
            <n-text depth="3" class="config-help">{{ t('codex.pluginProxyUrlHelp') }}</n-text>
          </div>
        </n-form-item-gi>

        <n-form-item-gi :span="24" :label="`${t('codex.baseUrl')}:`">
          <div class="config-field">
            <n-input v-model:value="form.baseURL" :placeholder="t('codex.baseUrlPlaceholder')" />
            <n-text depth="3" class="config-help">{{ t('codex.baseUrlHelp') }}</n-text>
          </div>
        </n-form-item-gi>

        <n-form-item-gi :span="24" :label="`${t('codex.clientVersion')}:`">
          <div class="config-field">
            <n-input v-model:value="form.clientVersion" :placeholder="t('codex.clientVersionPlaceholder')" />
            <n-text depth="3" class="config-help">{{ t('codex.clientVersionHelp') }}</n-text>
          </div>
        </n-form-item-gi>

        <n-form-item-gi :span="24" :label="`${t('codex.userAgent')}:`">
          <div class="config-field">
            <n-input v-model:value="form.userAgent" :placeholder="t('codex.userAgentPlaceholder')" />
            <n-text depth="3" class="config-help">{{ t('codex.userAgentHelp') }}</n-text>
          </div>
        </n-form-item-gi>

        <n-form-item-gi :span="24" :label="`${t('codex.originator')}:`">
          <div class="config-field">
            <n-input v-model:value="form.originator" :placeholder="t('codex.originatorPlaceholder')" />
            <n-text depth="3" class="config-help">{{ t('codex.originatorHelp') }}</n-text>
          </div>
        </n-form-item-gi>

        <n-form-item-gi :span="24">
          <n-divider class="config-divider" />
        </n-form-item-gi>

        <n-form-item-gi :span="24" :label="`${t('codex.customHeaders')}:`">
          <div class="config-field">
            <div class="array-editor-header">
              <n-text depth="3" class="config-help">{{ t('codex.customHeadersHelp') }}</n-text>
              <n-button size="small" type="primary" secondary @click="addCustomHeader">+</n-button>
            </div>
            <div
              v-for="(header, index) in customHeaderRows"
              :key="`custom-header-${index}`"
              class="custom-header-row"
            >
              <n-input
                v-model:value="customHeaderRows[index].key"
                :placeholder="t('codex.customHeaderNamePlaceholder')"
              />
              <n-input
                v-model:value="customHeaderRows[index].value"
                :placeholder="t('codex.customHeaderValuePlaceholder')"
              />
              <n-button size="small" type="error" secondary @click="removeCustomHeaderAt(index)">×</n-button>
            </div>
          </div>
        </n-form-item-gi>
      </n-grid>
    </n-form>

    <template #footer>
      <n-space justify="end">
        <n-button @click="close">{{ t('common.cancel') }}</n-button>
        <n-button type="primary" :loading="saving" @click="save">{{ t('common.save') }}</n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import {
  NModal,
  NForm,
  NFormItemGi,
  NInput,
  NButton,
  NDivider,
  NSpace,
  NGrid,
  NSelect,
  NText,
  useMessage
} from 'naive-ui'
import { useI18n } from 'vue-i18n'
import { codexApi } from '@/api/codex'
import type { CodexGlobalConfig } from '@/types/codex'

const DEFAULT_BASE_URL = 'https://chatgpt.com/backend-api/codex'

interface HeaderRow {
  key: string
  value: string
}

const { t } = useI18n()
const message = useMessage()

const props = withDefaults(
  defineProps<{
    show: boolean
  }>(),
  {
    show: false
  }
)

const emit = defineEmits<{
  'update:show': [show: boolean]
  saved: []
}>()

const visible = ref(false)
const saving = ref(false)
const customHeaderRows = ref<HeaderRow[]>([])

const form = reactive<CodexGlobalConfig>({
  rotationMode: 'fixed',
  proxyUrl: '',
  baseURL: DEFAULT_BASE_URL,
  clientVersion: '',
  userAgent: '',
  originator: '',
  customHeaders: {}
})

const rotationModeOptions = computed(() => [
  { label: t('codex.rotationModeFixed'), value: 'fixed' },
  { label: t('codex.rotationModeFailover'), value: 'failover' },
  { label: t('codex.rotationModeLoadBalance'), value: 'loadbalance' }
])

function fillForm(config: CodexGlobalConfig): void {
  form.rotationMode = config.rotationMode || 'fixed'
  form.proxyUrl = config.proxyUrl || ''
  form.baseURL = config.baseURL || DEFAULT_BASE_URL
  form.clientVersion = config.clientVersion || ''
  form.userAgent = config.userAgent || ''
  form.originator = config.originator || ''
  form.customHeaders = normalizeCustomHeaders(config.customHeaders)
  customHeaderRows.value = customHeadersToRows(form.customHeaders)
}

function normalizeCustomHeaders(headers: Record<string, string> | undefined): Record<string, string> {
  const normalized: Record<string, string> = {}
  Object.entries(headers || {}).forEach(([key, value]) => {
    const name = String(key || '').trim()
    const headerValue = String(value || '').trim()
    if (name && headerValue) normalized[name] = headerValue
  })
  return normalized
}

function customHeadersToRows(headers: Record<string, string> | undefined): HeaderRow[] {
  return Object.entries(headers || {})
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([key, value]) => ({ key, value }))
}

function rowsToCustomHeaders(rows: HeaderRow[]): Record<string, string> {
  const headers: Record<string, string> = {}
  rows.forEach((row) => {
    const key = row.key.trim()
    const value = row.value.trim()
    if (key && value) headers[key] = value
  })
  return headers
}

function addCustomHeader(): void {
  customHeaderRows.value.push({ key: '', value: '' })
}

function removeCustomHeaderAt(index: number): void {
  if (index < 0 || index >= customHeaderRows.value.length) return
  customHeaderRows.value.splice(index, 1)
}

function toErrorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return String(error)
}

watch(
  () => props.show,
  async (show) => {
    visible.value = show
    if (!show) return

    try {
      const config = await codexApi.getGlobalConfig()
      fillForm(config)
    } catch (error) {
      message.error(t('codex.loadConfigFailed') + toErrorMessage(error))
    }
  },
  { immediate: true }
)

watch(visible, (show) => {
  if (!show) emit('update:show', false)
})

function close(): void {
  visible.value = false
}

async function save(): Promise<void> {
  saving.value = true
  try {
    const payload: CodexGlobalConfig = {
      rotationMode: String(form.rotationMode || 'fixed').trim(),
      proxyUrl: String(form.proxyUrl || '').trim(),
      baseURL: String(form.baseURL || '').trim(),
      clientVersion: String(form.clientVersion || '').trim(),
      userAgent: String(form.userAgent || '').trim(),
      originator: String(form.originator || '').trim(),
      customHeaders: rowsToCustomHeaders(customHeaderRows.value)
    }
    await codexApi.saveGlobalConfig(payload)
    message.success(t('codex.globalConfigSaved'))
    emit('saved')
    close()
  } catch (error) {
    message.error(t('codex.globalConfigSaveFailed') + toErrorMessage(error))
  } finally {
    saving.value = false
  }
}
</script>

<style scoped>
.config-field {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.config-help {
  line-height: 1.35;
}

.config-divider {
  margin: 2px 0;
}

.array-editor-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.custom-header-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) auto;
  gap: 8px;
  width: 100%;
}
</style>
