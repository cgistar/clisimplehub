<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { storeToRefs } from 'pinia'
import { Pencil } from 'lucide-vue-next'
import {
  NAutoComplete,
  NButton,
  NDivider,
  NForm,
  NFormItem,
  NInput,
  NInputNumber,
  NModal,
  NSelect,
  NSwitch
} from 'naive-ui'
import { endpointApi } from '@/api/endpoint'
import type {
  Endpoint,
  EndpointInputPayload,
  InterfaceType,
  ModelMappingInput,
  TestEndpointResult
} from '@/types/endpoint'
import { useHomeEndpointsStore } from '@/stores/homeEndpointsStore'
import { useVendorsStore } from '@/stores/vendorsStore'
import { useFeedback } from '@/composables/useFeedback'

interface EndpointFormState {
  id: number
  providerName: string
  name: string
  apiUrl: string
  apiKey: string
  interfaceType: InterfaceType
  model: string
  transformer: string
  proxyUrl: string
  priority: number
  enabled: boolean
  active: boolean
  remark: string
  models: ModelMappingInput[]
  routes: string[]
}

const CLAUDE_QUICK_MAPPINGS: ModelMappingInput[] = [
  { alias: 'claude-haiku-4-5-20251001', name: 'claude-4.5-haiku' },
  { alias: 'claude-opus-4-5-20251101', name: 'claude-4.5-opus' },
  { alias: 'claude-sonnet-4-5-20250929', name: 'claude-4.5-sonnet' }
]

const interfaceOptions: Array<{ label: string; value: InterfaceType }> = [
  { label: 'Claude', value: 'claude' },
  { label: 'Codex', value: 'codex' },
  { label: 'Gemini', value: 'gemini' },
  { label: 'Chat', value: 'chat' }
]

const { t } = useI18n()
const emit = defineEmits<{
  'manage-vendors': []
}>()
const endpointStore = useHomeEndpointsStore()
const vendorsStore = useVendorsStore()
const feedback = useFeedback()
const { vendors, loading: vendorsLoading } = storeToRefs(vendorsStore)

const visible = ref(false)
const saving = ref(false)
const testing = ref(false)
const fetchingModels = ref(false)
const fetchedModels = ref<string[]>([])
const transformerMap = ref<Record<string, string[]>>({})
const form = ref<EndpointFormState>(createDefaultForm())

const isEditing = computed(() => form.value.id > 0)
const formTitle = computed(() => (isEditing.value ? t('manage.editEndpoint') : t('manage.addEndpoint')))
const canTest = computed(() => {
  if (form.value.interfaceType === 'codex') return true
  if (form.value.interfaceType === 'claude') return form.value.model.trim().length > 0
  return false
})
const showQuickMapping = computed(() => form.value.interfaceType === 'claude')
const availableTransformers = computed(() => transformerMap.value[form.value.interfaceType] || [])
const vendorOptions = computed<Array<{ label: string; value: string }>>(() =>
  vendors.value.map((item) => ({
    label: item.name,
    value: item.name
  }))
)
const transformerOptions = computed<Array<{ label: string; value: string }>>(() => [
  { label: t('manage.transformerNone'), value: '' },
  ...availableTransformers.value.map((item) => ({ label: item, value: item }))
])
const modelOptions = computed<Array<{ label: string; value: string }>>(() =>
  Array.from(new Set([form.value.model.trim(), ...fetchedModels.value]))
    .filter((model) => model.length > 0)
    .map((model) => ({
      label: model,
      value: model
    }))
)

function createDefaultForm(interfaceType: InterfaceType = endpointStore.currentTab): EndpointFormState {
  return {
    id: 0,
    providerName: '',
    name: '',
    apiUrl: '',
    apiKey: '',
    interfaceType,
    model: '',
    transformer: '',
    proxyUrl: '',
    priority: 5,
    enabled: true,
    active: false,
    remark: '',
    models: [],
    routes: []
  }
}

function toErrorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return String(error)
}

function normalizeMappings(items: ModelMappingInput[] | undefined): ModelMappingInput[] {
  if (!items || items.length === 0) return []
  return items
    .map((item) => ({
      alias: item.alias?.trim() || '',
      name: item.name?.trim() || ''
    }))
    .filter((item) => item.alias.length > 0 || item.name.length > 0)
}

function normalizeRoutes(items: string[] | undefined): string[] {
  if (!items || items.length === 0) return []
  return items.map((item) => item.trim()).filter((item) => item.length > 0)
}

function fillForm(endpoint: Endpoint): void {
  form.value = {
    id: endpoint.id || 0,
    providerName: endpoint.providerName || '',
    name: endpoint.name || '',
    apiUrl: endpoint.apiUrl || '',
    apiKey: endpoint.apiKey || '',
    interfaceType: (endpoint.interfaceType as InterfaceType) || 'claude',
    model: endpoint.model || '',
    transformer: endpoint.transformer || '',
    proxyUrl: endpoint.proxyUrl || '',
    priority: endpoint.priority || 5,
    enabled: endpoint.enabled !== false,
    active: !!endpoint.active,
    remark: endpoint.remark || '',
    models: normalizeMappings(endpoint.models),
    routes: normalizeRoutes(endpoint.routes)
  }
  fetchedModels.value = []
}

async function ensureTransformersLoaded(force = false): Promise<void> {
  if (!force && Object.keys(transformerMap.value).length > 0) return
  transformerMap.value = await endpointApi.getTransformers()
}

async function ensureVendorsLoaded(force = false): Promise<void> {
  if (!force && vendors.value.length > 0) return
  await vendorsStore.loadVendors()
}

async function refreshEndpointPanels(): Promise<void> {
  await endpointStore.refreshCurrent()
}

function onEndpointInterfaceTypeChange(): void {
  form.value.transformer = ''
  fetchedModels.value = []
}

function onVendorChange(value: string | number | null): void {
  const vendorName = typeof value === 'string' ? value : ''
  form.value.providerName = vendorName
  if (!vendorName) return

  const selected = vendors.value.find((item) => item.name === vendorName)
  if (selected?.apiUrl) {
    form.value.apiUrl = selected.apiUrl
  }
}

function openVendorManage(): void {
  emit('manage-vendors')
}

function toggleApiKeyVisibility(): void {
  // Kept for compatibility with legacy window.* API.
}

function addModelMapping(): void {
  form.value.models.push({ alias: '', name: '' })
}

function removeModelMappingAt(index: number): void {
  if (index < 0 || index >= form.value.models.length) return
  form.value.models.splice(index, 1)
}

function addRoute(): void {
  form.value.routes.push('')
}

function removeRouteAt(index: number): void {
  if (index < 0 || index >= form.value.routes.length) return
  form.value.routes.splice(index, 1)
}

function applyQuickModelMappings(): void {
  const existingAliases = new Set(
    form.value.models.map((item) => item.alias.trim()).filter((item) => item.length > 0)
  )

  let addedCount = 0
  CLAUDE_QUICK_MAPPINGS.forEach((mapping) => {
    if (existingAliases.has(mapping.alias)) return
    form.value.models.push({ ...mapping })
    existingAliases.add(mapping.alias)
    addedCount += 1
  })

  if (addedCount > 0) {
    feedback.success(`已添加 ${addedCount} 个映射`)
  } else {
    feedback.success('映射已存在，无需添加')
  }
}

async function fetchModels(): Promise<void> {
  const apiUrl = form.value.apiUrl.trim()
  const apiKey = form.value.apiKey.trim()

  if (!apiUrl) {
    feedback.error(t('manage.fetchModelsNoUrl'))
    return
  }
  if (!apiKey) {
    feedback.error(t('manage.fetchModelsNoKey'))
    return
  }

  try {
    fetchingModels.value = true
    const result = await endpointApi.fetchModels(apiUrl, apiKey, form.value.interfaceType)

    if (result.success && result.models.length > 0) {
      fetchedModels.value = Array.from(new Set(result.models.map((item) => item.trim()).filter((item) => item.length > 0)))
      if (fetchedModels.value.length > 0) {
        const currentModel = form.value.model.trim()
        if (!currentModel || !fetchedModels.value.includes(currentModel)) {
          form.value.model = fetchedModels.value[0]
        }
      }
      feedback.success(t('manage.fetchModelsSuccess').replace('{count}', String(result.models.length)))
    } else {
      const message = result.message?.includes('no_models_found')
        ? t('manage.fetchModelsEmpty')
        : t('manage.fetchModelsFailed')
      feedback.error(message)
    }
  } catch (error) {
    feedback.error(t('manage.fetchModelsFailed') + ': ' + toErrorMessage(error))
  } finally {
    fetchingModels.value = false
  }
}

function normalizeTestResult(result: TestEndpointResult): TestEndpointResult {
  return {
    success: !!result.success,
    message: result.message || '',
    statusCode: result.statusCode,
    targetUrl: result.targetUrl,
    requestHeaders: result.requestHeaders,
    errorMessage: result.errorMessage,
    responseText: result.responseText
  }
}

async function testEndpoint(): Promise<void> {
  const apiUrl = form.value.apiUrl.trim()
  const apiKey = form.value.apiKey.trim()

  if (!apiUrl || !apiKey) {
    feedback.error('Please fill in API URL and API Key first')
    return
  }

  try {
    testing.value = true
    const result = normalizeTestResult(await endpointApi.testEndpointWithParams({
      apiUrl,
      apiKey,
      interfaceType: form.value.interfaceType,
      model: form.value.model.trim()
    }))

    if (result.success) {
      feedback.success(t('manage.testSuccess') + ': ' + result.message)
    } else {
      feedback.error(t('manage.testFailed') + ': ' + (result.errorMessage || result.message))
    }
  } catch (error) {
    feedback.error(t('manage.testFailed') + ': ' + toErrorMessage(error))
  } finally {
    testing.value = false
  }
}

function buildSavePayload(): EndpointInputPayload {
  const models = normalizeMappings(form.value.models)
  const routes = normalizeRoutes(form.value.routes)
  const priority = Number(form.value.priority)
  const normalizedPriority = Number.isFinite(priority) && priority > 0 ? priority : 5

  return {
    id: form.value.id,
    name: form.value.name.trim(),
    apiUrl: form.value.apiUrl.trim(),
    apiKey: form.value.apiKey.trim(),
    interfaceType: form.value.interfaceType,
    model: form.value.model.trim(),
    transformer: form.value.transformer.trim(),
    transformerSet: true,
    proxyUrl: form.value.proxyUrl.trim(),
    proxyUrlSet: true,
    providerName: form.value.providerName.trim(),
    models: models.length > 0 ? models : null,
    modelsSet: true,
    routes: routes.length > 0 ? routes : null,
    routesSet: true,
    remark: form.value.remark.trim(),
    priority: normalizedPriority,
    enabled: form.value.enabled,
    active: false
  }
}

async function save(): Promise<void> {
  const payload = buildSavePayload()
  if (!payload.name || !payload.apiUrl) {
    feedback.error('Please fill in all required fields')
    return
  }
  if (!isEditing.value && !payload.apiKey) {
    feedback.error('API Key is required for new endpoints')
    return
  }

  try {
    saving.value = true
    await endpointApi.saveEndpoint(payload)
    close()
    await refreshEndpointPanels()
    feedback.success('Endpoint saved successfully')
  } catch (error) {
    feedback.error(t('manage.saveFailed') + ': ' + toErrorMessage(error))
  } finally {
    saving.value = false
  }
}

async function runDelete(endpointId: number, source: 'form' | 'list'): Promise<void> {
  if (!endpointId) return

  const confirmed = await feedback.confirm(t('manage.confirmDeleteEndpoint') || 'Confirm delete endpoint?', {
    danger: true
  })
  if (!confirmed) return

  try {
    await endpointApi.deleteEndpoint(endpointId)

    if (source === 'form') {
      close()
    }

    await refreshEndpointPanels()
    feedback.success('Endpoint deleted successfully')
  } catch (error) {
    feedback.error(t('manage.deleteFailed') + ': ' + toErrorMessage(error))
  }
}

async function deleteCurrent(): Promise<void> {
  await runDelete(form.value.id, 'form')
}

async function deleteById(endpointId: number): Promise<void> {
  await runDelete(endpointId, 'list')
}

function close(): void {
  visible.value = false
  fetchedModels.value = []
}

async function open(endpoint: Endpoint | null = null): Promise<void> {
  try {
    await Promise.all([
      ensureTransformersLoaded(true),
      ensureVendorsLoaded(true)
    ])
    form.value = createDefaultForm(endpointStore.currentTab)

    if (endpoint && endpoint.id > 0) {
      const full = await endpointApi.getById(endpoint.id)
      fillForm(full)
    } else if (endpoint) {
      fillForm(endpoint)
    }

    visible.value = true
  } catch (error) {
    feedback.error('Failed to load endpoint: ' + toErrorMessage(error))
  }
}

async function editById(endpointId: number): Promise<void> {
  try {
    const endpoint = await endpointApi.getById(endpointId)
    await open(endpoint)
  } catch (error) {
    feedback.error('Failed to load endpoint: ' + toErrorMessage(error))
  }
}

function toggleModelDropdown(): void {
  // Kept for compatibility with legacy window.* API.
}

function toggleTransformerDropdown(): void {
  // Kept for compatibility with legacy window.* API.
}

function toggleVendorDropdown(): void {
  // Kept for compatibility with legacy window.* API.
}

function toggleInterfaceTypeDropdown(): void {
  // Kept for compatibility with legacy window.* API.
}

function updateTestButtonVisibility(): void {
  // Vue template handles this via computed state.
}

defineExpose({
  open,
  editById,
  close,
  save,
  deleteCurrent,
  deleteById,
  toggleApiKeyVisibility,
  onEndpointInterfaceTypeChange,
  updateTestButtonVisibility,
  testEndpoint,
  fetchModels,
  toggleModelDropdown,
  toggleTransformerDropdown,
  toggleVendorDropdown,
  toggleInterfaceTypeDropdown,
  addModelMapping,
  removeModelMappingAt,
  addRoute,
  removeRouteAt,
  applyQuickModelMappings
})
</script>

<template>
  <n-modal
    v-model:show="visible"
    preset="card"
    class="endpoint-form-modal"
    :title="formTitle"
    :style="{
      width: 'min(760px, 94vw)',
      height: '88vh',
      maxHeight: '88vh',
      display: 'flex',
      flexDirection: 'column',
      overflow: 'hidden'
    }"
    :header-style="{ flexShrink: 0 }"
    :footer-style="{ flexShrink: 0 }"
    :content-style="{ flex: 1, minHeight: 0, overflowY: 'auto', paddingRight: '0px' }"
    :closable="true"
    :mask-closable="false"
    :close-on-esc="true"
    :block-scroll="true"
    @close="close"
  >
    <n-form
        :model="form"
        label-placement="left"
        :label-width="132"
        label-align="right"
        class="endpoint-form"
      >
      <div class="endpoint-form-grid">
        <n-form-item :label="t('manage.vendor')">
          <div class="vendor-field-wrap">
            <div class="vendor-field">
              <n-select
                :value="form.providerName || null"
                class="vendor-select"
                :options="vendorOptions"
                :placeholder="t('manage.selectVendor')"
                :loading="vendorsLoading"
                clearable
                @update:value="onVendorChange"
              />
              <n-button
                quaternary
                circle
                :title="t('manage.editVendor')"
                @click="openVendorManage"
              >
                <Pencil :size="14" :stroke-width="2" />
              </n-button>
            </div>
            <span class="help-text">{{ t('manage.vendorHelp') }}</span>
          </div>
        </n-form-item>

        <n-form-item :label="`${t('manage.endpointName')} *`">
          <n-input
            v-model:value="form.name"
            :placeholder="t('manage.endpointNamePlaceholder')"
          />
        </n-form-item>

        <n-form-item :label="`${t('manage.apiUrl')} *`">
          <n-input
            v-model:value="form.apiUrl"
            :placeholder="t('manage.apiUrlPlaceholder')"
          />
        </n-form-item>

        <n-form-item :label="`${t('manage.apiKey')} *`">
          <n-input
            v-model:value="form.apiKey"
            type="password"
            show-password-on="click"
            :placeholder="t('manage.apiKeyPlaceholder')"
          />
        </n-form-item>

        <n-form-item :label="`${t('manage.interfaceType')} *`">
          <n-select
            v-model:value="form.interfaceType"
            :options="interfaceOptions"
            @update:value="onEndpointInterfaceTypeChange"
          />
        </n-form-item>

        <n-form-item :label="t('manage.model')">
          <div class="model-field">
            <div class="model-editor">
              <n-auto-complete
                v-model:value="form.model"
                class="model-input"
                :options="modelOptions"
                :placeholder="t('manage.modelPlaceholder')"
                clearable
                :blur-after-select="true"
              />
              <n-button
                :loading="fetchingModels"
                secondary
                @click="fetchModels"
              >
                {{ t('manage.fetchModelsBtn') }}
              </n-button>
              <n-button
                v-if="canTest"
                :loading="testing"
                secondary
                @click="testEndpoint"
              >
                {{ testing ? t('manage.testing') : t('manage.test') }}
              </n-button>
            </div>
            <span class="help-text">{{ t('manage.modelHelp') }}</span>
          </div>
        </n-form-item>

      <n-form-item :label="t('manage.routes')">
        <div class="array-editor">
          <div class="array-editor-header">
            <span class="help-text">{{ t('manage.routesHelp') }}</span>
            <n-button size="small" type="primary" secondary @click="addRoute">+</n-button>
          </div>
          <div v-for="(route, index) in form.routes" :key="`route-${index}`" class="array-row">
            <n-input
              v-model:value="form.routes[index]"
              :placeholder="t('manage.routePlaceholder')"
            />
            <n-button size="small" type="error" secondary @click="removeRouteAt(index)">×</n-button>
          </div>
        </div>
      </n-form-item>

      <n-divider />

        <n-form-item :label="t('manage.transformer')">
          <div class="model-editor">
            <n-select
              v-model:value="form.transformer"
              class="model-input"
              :options="transformerOptions"
            />
            <n-button
              v-if="showQuickMapping"
              type="primary"
              secondary
              :title="t('manage.quickMappingTitle')"
              @click="applyQuickModelMappings"
            >
              {{ t('manage.quickMapping') }}
            </n-button>
          </div>
        </n-form-item>

      <n-form-item :label="t('manage.modelMappings')">
        <div class="array-editor">
          <div class="array-editor-header">
            <span class="help-text">{{ t('manage.modelMappingsHelp') }}</span>
            <n-button size="small" type="primary" secondary @click="addModelMapping">+</n-button>
          </div>
          <div v-for="(mapping, index) in form.models" :key="`mapping-${index}`" class="array-row array-row-mapping">
            <n-input
              v-model:value="form.models[index].alias"
              :placeholder="t('manage.modelMappingAlias')"
            />
            <n-input
              v-model:value="form.models[index].name"
              :placeholder="t('manage.modelMappingName')"
            />
            <n-button size="small" type="error" secondary @click="removeModelMappingAt(index)">×</n-button>
          </div>
        </div>
      </n-form-item>

      <n-divider />

        <n-form-item :label="t('manage.proxyUrl')">
          <n-input
            v-model:value="form.proxyUrl"
            :placeholder="t('manage.proxyUrlPlaceholder')"
          />
        </n-form-item>

      </div>

      <n-form-item :label="t('manage.priority')">
        <n-input-number
          v-model:value="form.priority"
          :min="1"
          :max="10"
          :precision="0"
          style="width: 100%"
        />
      </n-form-item>

      <n-form-item :label="t('manage.status')">
        <div class="status-field">
          <span>{{ t('manage.enabled') }}</span>
          <n-switch
            v-model:value="form.enabled"
            :disabled="form.active"
          />
        </div>
      </n-form-item>

      <n-form-item :label="t('manage.remark')">
        <n-input
          v-model:value="form.remark"
          :placeholder="t('manage.remarkPlaceholder')"
        />
      </n-form-item>
      </n-form>

    <template #footer>
      <div class="modal-footer-actions">
        <n-button
          v-if="isEditing"
          type="error"
          secondary
          class="modal-footer-delete"
          @click="deleteCurrent"
        >
          {{ t('manage.delete') }}
        </n-button>
        <div class="modal-footer-main-actions">
          <n-button :disabled="saving" @click="close">{{ t('manage.cancel') }}</n-button>
          <n-button type="primary" :loading="saving" @click="save">{{ t('manage.save') }}</n-button>
        </div>
      </div>
    </template>
  </n-modal>
</template>

<style scoped>
.endpoint-form {
  padding-right: 24px;
  box-sizing: border-box;
}

.endpoint-form-grid {
  display: grid;
  grid-template-columns: 1fr;
  gap: 8px 12px;
}

.endpoint-form :deep(.n-form-item) {
  align-items: flex-start;
}

.endpoint-form :deep(.n-form-item-label) {
  display: flex;
  justify-content: flex-end;
  padding-right: 12px;
  box-sizing: border-box;
}

.endpoint-form :deep(.n-form-item-label > label) {
  width: 100%;
  text-align: right;
  line-height: 34px;
}

.endpoint-form :deep(.n-form-item-blank) {
  min-width: 0;
}

.help-text {
  margin-top: 6px;
  font-size: 12px;
  color: #64748b;
}

.vendor-field-wrap {
  display: flex;
  flex-direction: column;
  width: 100%;
}

.vendor-field {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
}

.vendor-select {
  flex: 1;
  min-width: 0;
}

.model-editor {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
}

.model-field {
  display: flex;
  flex-direction: column;
  width: 100%;
}

.model-input {
  flex: 1;
  min-width: 0;
}

.array-editor {
  display: flex;
  flex-direction: column;
  gap: 8px;
  width: 100%;
}

.array-editor-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.array-row {
  display: grid;
  grid-template-columns: 1fr auto;
  gap: 8px;
  width: 100%;
}

.array-row-mapping {
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) auto;
}

.modal-footer-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
}

.modal-footer-delete {
  margin-right: auto;
}

.modal-footer-main-actions {
  margin-left: auto;
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
}

.status-field {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-height: 34px;
}
</style>
