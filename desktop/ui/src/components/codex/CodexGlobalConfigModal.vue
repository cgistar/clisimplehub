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
const DEFAULT_ORIGINATOR = 'codex_cli_rs'

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

const form = reactive<CodexGlobalConfig>({
  rotationMode: 'fixed',
  proxyUrl: '',
  baseURL: DEFAULT_BASE_URL,
  clientVersion: '',
  userAgent: '',
  originator: DEFAULT_ORIGINATOR
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
  form.originator = config.originator || DEFAULT_ORIGINATOR
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
      originator: String(form.originator || '').trim()
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
</style>
