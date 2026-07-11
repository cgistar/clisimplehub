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
import { NModal, NForm, NFormItem, NSelect, NInput, NButton, NSpace, useMessage } from 'naive-ui'
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
  baseURL: 'https://api.x.ai/v1'
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
      baseURL: form.baseURL
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
