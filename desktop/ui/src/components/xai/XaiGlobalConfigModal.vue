<template>
  <n-modal
    v-model:show="visible"
    preset="card"
    :title="t('xai.configModalTitle')"
    style="width: 640px"
  >
    <n-form label-placement="left" label-width="120" :model="form">
      <n-form-item :label="t('xai.rotationMode')">
        <n-select v-model:value="form.rotationMode" :options="rotationModeOptions" />
      </n-form-item>
      <n-form-item :label="t('xai.proxyUrl')">
        <n-input v-model:value="form.proxyUrl" :placeholder="t('xai.proxyUrlPlaceholder')" />
      </n-form-item>
      <n-form-item :label="t('xai.baseURL')">
        <n-input v-model:value="form.baseURL" :placeholder="t('xai.baseURLPlaceholder')" />
      </n-form-item>
      <n-form-item :label="t('xai.clientVersion')">
        <n-input v-model:value="form.clientVersion" :placeholder="t('xai.clientVersionPlaceholder')" />
      </n-form-item>
      <n-form-item :label="t('xai.userAgent')">
        <n-input v-model:value="form.userAgent" :placeholder="t('xai.userAgentPlaceholder')" />
      </n-form-item>
      <n-form-item :label="t('xai.dynamicStatsig')">
        <div class="dynamic-statsig-row">
          <n-switch v-model:value="form.dynamicStatsig" />
          <span class="dynamic-statsig-help">{{ t('xai.dynamicStatsigHelp') }}</span>
        </div>
      </n-form-item>
    </n-form>
    <template #footer>
      <n-space justify="end">
        <n-button @click="visible = false">{{ t('common.cancel') }}</n-button>
        <n-button type="primary" :loading="saving" @click="save">{{ t('common.save') }}</n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { NModal, NForm, NFormItem, NSelect, NInput, NButton, NSpace, NSwitch, useMessage } from 'naive-ui'
import { useI18n } from 'vue-i18n'
import { xaiApi } from '@/api/xai'

const { t } = useI18n()
const message = useMessage()

const props = withDefaults(defineProps<{ show: boolean }>(), { show: false })
const emit = defineEmits<{ 'update:show': [show: boolean] }>()

const visible = ref(false)
const saving = ref(false)
const form = reactive({
  rotationMode: 'fixed',
  proxyUrl: '',
  baseURL: 'https://api.x.ai/v1',
  clientVersion: '',
  userAgent: '',
  tokenAuth: '',
  clientSurface: '',
  dynamicStatsig: true
})

const rotationModeOptions = computed(() => [
  { label: t('xai.rotationFixed'), value: 'fixed' },
  { label: t('xai.rotationFailover'), value: 'failover' },
  { label: t('xai.rotationLoadBalance'), value: 'loadbalance' }
])

watch(() => props.show, async (v) => {
  visible.value = v
  if (v) await loadConfig()
})
watch(visible, (v) => {
  if (!v) emit('update:show', false)
})

async function loadConfig() {
  try {
    const config = await xaiApi.getGlobalConfig()
    form.rotationMode = config.rotationMode || 'fixed'
    form.proxyUrl = config.proxyUrl || ''
    form.baseURL = config.baseURL || 'https://api.x.ai/v1'
    form.clientVersion = config.clientVersion || ''
    form.userAgent = config.userAgent || ''
    form.tokenAuth = config.tokenAuth || ''
    form.clientSurface = config.clientSurface || ''
    form.dynamicStatsig = config.dynamicStatsig !== false
  } catch (error) {
    message.error(t('xai.loadConfigFailed') + (error instanceof Error ? error.message : String(error)))
  }
}

async function save() {
  saving.value = true
  try {
    await xaiApi.saveGlobalConfig({
      rotationMode: form.rotationMode,
      proxyUrl: form.proxyUrl,
      baseURL: form.baseURL,
      clientVersion: form.clientVersion,
      userAgent: form.userAgent,
      tokenAuth: form.tokenAuth,
      clientSurface: form.clientSurface,
      dynamicStatsig: form.dynamicStatsig !== false
    })
    message.success(t('xai.globalConfigSaved'))
    visible.value = false
  } catch (error) {
    message.error(t('xai.globalConfigSaveFailed') + (error instanceof Error ? error.message : String(error)))
  } finally {
    saving.value = false
  }
}
</script>

<style scoped>
.dynamic-statsig-row {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
}
.dynamic-statsig-help {
  font-size: 12px;
  color: var(--text-tertiary, #8a97a8);
  line-height: 1.4;
}
</style>
