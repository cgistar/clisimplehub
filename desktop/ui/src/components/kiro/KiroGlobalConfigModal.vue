<template>
  <n-modal
    v-model:show="visible"
    preset="card"
    class="kiro-global-config-modal"
    :title="t('kiro.globalConfig')"
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
  >
    <n-form label-placement="top" :model="form" class="kiro-global-config-form">
      <n-grid :cols="24" :x-gap="12">
        <n-form-item-gi :span="12" :label="t('kiro.region')">
          <n-input v-model:value="form.region" placeholder="us-east-1" />
        </n-form-item-gi>

        <n-form-item-gi :span="12" :label="t('kiro.rotationMode')">
          <n-select v-model:value="form.rotationMode" :options="rotationModeOptions" />
        </n-form-item-gi>

        <n-form-item-gi :span="12" :label="t('kiro.proxyUrl')">
          <n-input v-model:value="form.proxyUrl" :placeholder="t('kiro.proxyUrlPlaceholder')" />
        </n-form-item-gi>

        <n-form-item-gi :span="12" :label="t('kiro.userAgent')">
          <n-input v-model:value="form.userAgent" :placeholder="t('kiro.userAgentPlaceholder')" />
        </n-form-item-gi>

        <n-form-item-gi :span="12" :label="t('kiro.version')">
          <n-input v-model:value="form.version" :placeholder="t('kiro.versionPlaceholder')" />
        </n-form-item-gi>

        <n-form-item-gi :span="12" :label="t('kiro.bufferedStream')">
          <n-switch v-model:value="form.bufferedStream" />
        </n-form-item-gi>

        <n-form-item-gi :span="24">
          <n-space justify="space-between" align="center">
            <n-text>{{ t('kiro.modelMappingHelp') }}</n-text>
            <n-space>
              <n-button size="small" @click="addMappingRow">{{ t('kiro.addMapping') }}</n-button>
              <n-button size="small" @click="resetDefaults">{{ t('kiro.resetDefaults') }}</n-button>
            </n-space>
          </n-space>
        </n-form-item-gi>

        <n-form-item-gi :span="24">
          <n-space vertical :size="8" style="width: 100%">
            <div v-for="row in mappingRows" :key="row.id" class="mapping-row">
              <n-input v-model:value="row.alias" :placeholder="t('kiro.mappingAlias')" />
              <span class="mapping-arrow">→</span>
              <n-input v-model:value="row.name" :placeholder="t('kiro.mappingName')" />
              <n-button quaternary type="error" @click="removeMappingRow(row.id)">x</n-button>
            </div>
          </n-space>
        </n-form-item-gi>
      </n-grid>
    </n-form>

    <template #footer>
      <n-space justify="end">
        <n-button @click="close">{{ t('common.cancel') }}</n-button>
        <n-button type="primary" :loading="store.savingGlobalConfig" @click="save">{{ t('common.save') }}</n-button>
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
  NSpace,
  NGrid,
  NSelect,
  NSwitch,
  NText,
  useMessage
} from 'naive-ui'
import { useI18n } from 'vue-i18n'
import { useKiroConfigStore } from '@/stores/kiroConfigStore'
import type { KiroGlobalConfig } from '@/types/kiro'

interface MappingRow {
  id: string
  alias: string
  name: string
}

const { t } = useI18n()
const message = useMessage()
const store = useKiroConfigStore()

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

const form = reactive<KiroGlobalConfig>({
  region: 'us-east-1',
  proxyUrl: '',
  userAgent: '',
  version: '',
  bufferedStream: false,
  rotationMode: 'fixed',
  modelMapping: {}
})

const mappingRows = ref<MappingRow[]>([])

const rotationModeOptions = computed(() => [
  { label: t('kiro.rotationFixed'), value: 'fixed' },
  { label: t('kiro.rotationFailover'), value: 'failover' },
  { label: t('kiro.rotationLoadBalance'), value: 'loadbalance' }
])

function generateId(): string {
  return `${Date.now()}-${Math.random().toString(16).slice(2)}`
}

function fillFromStore(): void {
  const cfg = store.globalConfig
  form.region = cfg.region || 'us-east-1'
  form.proxyUrl = cfg.proxyUrl || ''
  form.userAgent = cfg.userAgent || ''
  form.version = cfg.version || ''
  form.bufferedStream = !!cfg.bufferedStream
  form.rotationMode = cfg.rotationMode || 'fixed'
  form.modelMapping = { ...(cfg.modelMapping || {}) }

  mappingRows.value = Object.entries(form.modelMapping).map(([alias, name]) => ({
    id: generateId(),
    alias,
    name
  }))
}

watch(
  () => props.show,
  async (show) => {
    visible.value = show
    if (!show) return

    try {
      await store.loadGlobalConfig()
      fillFromStore()
    } catch (error) {
      message.error(t('kiro.globalConfigSaveFailed') + String(error))
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

function addMappingRow(): void {
  mappingRows.value.push({
    id: generateId(),
    alias: '',
    name: ''
  })
}

function removeMappingRow(id: string): void {
  mappingRows.value = mappingRows.value.filter((row) => row.id !== id)
}

async function resetDefaults(): Promise<void> {
  try {
    const defaults = await store.resetGlobalModelMappingDefaults()
    mappingRows.value = Object.entries(defaults).map(([alias, name]) => ({
      id: generateId(),
      alias,
      name
    }))
    message.success(t('kiro.resetDefaultsSuccess'))
  } catch (error) {
    message.error(t('kiro.resetDefaultsFailed') + ': ' + String(error instanceof Error ? error.message : error))
  }
}

async function save(): Promise<void> {
  const mapping: Record<string, string> = {}
  for (const row of mappingRows.value) {
    const alias = String(row.alias || '').trim()
    const name = String(row.name || '').trim()
    if (!alias || !name) continue
    mapping[alias] = name
  }

  const payload: KiroGlobalConfig = {
    region: String(form.region || 'us-east-1').trim(),
    proxyUrl: String(form.proxyUrl || '').trim(),
    userAgent: String(form.userAgent || '').trim(),
    version: String(form.version || '').trim(),
    bufferedStream: !!form.bufferedStream,
    rotationMode: String(form.rotationMode || 'fixed').trim(),
    modelMapping: mapping
  }

  try {
    await store.saveCurrentGlobalConfig(payload)
    message.success(t('kiro.globalConfigSaved'))
    emit('saved')
    close()
  } catch (error) {
    message.error(t('kiro.globalConfigSaveFailed') + String(error instanceof Error ? error.message : error))
  }
}
</script>

<style scoped>
.kiro-global-config-form {
  padding-right: 24px;
  box-sizing: border-box;
}

.mapping-row {
  display: grid;
  grid-template-columns: 1fr auto 1fr auto;
  align-items: center;
  gap: 8px;
}

.mapping-arrow {
  color: var(--text-secondary);
}
</style>
