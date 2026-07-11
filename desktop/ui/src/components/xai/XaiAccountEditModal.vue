<template>
  <n-modal
    v-model:show="visible"
    preset="card"
    :title="isCreate ? t('xai.addApiKeyTitle') : t('xai.editAccountTitle')"
    style="width: 520px"
  >
    <n-form :model="formData">
      <n-form-item v-if="isCreate || formData.authKind === 'api_key'" :label="t('xai.apiKeyLabel')">
        <n-input
          v-model:value="formData.apiKey"
          type="textarea"
          :rows="2"
          :placeholder="t('xai.apiKeyPlaceholder')"
        />
      </n-form-item>

      <n-form-item v-if="!isCreate && formData.authKind !== 'api_key'" :label="t('xai.refreshTokenLabel')">
        <n-input v-model:value="formData.refreshToken" type="textarea" :rows="2" readonly disabled />
      </n-form-item>

      <n-form-item :label="t('xai.emailLabel')">
        <n-input v-model:value="formData.email" :placeholder="t('xai.emailPlaceholder')" />
      </n-form-item>

      <n-form-item :label="t('xai.proxyUrl')">
        <n-input v-model:value="formData.proxyUrl" :placeholder="t('xai.proxyUrlPlaceholder')" />
      </n-form-item>

      <n-form-item :label="t('xai.weight')">
        <n-input-number v-model:value="formData.weight" :min="1" :max="100" style="width: 100%" />
      </n-form-item>

      <n-form-item :label="t('xai.baseURL')">
        <n-input v-model:value="formData.baseURL" :placeholder="t('xai.baseURLPlaceholder')" />
      </n-form-item>

      <n-form-item :label="t('xai.accountCapabilityLabel')">
        <n-space vertical>
          <n-space align="center" justify="space-between">
            <span>{{ t('xai.enabledLabel') }}</span>
            <n-switch v-model:value="formData.enabled" />
          </n-space>
          <n-space align="center" justify="space-between">
            <span>{{ t('xai.websocketsLabel') }}</span>
            <n-switch v-model:value="formData.websockets" />
          </n-space>
        </n-space>
      </n-form-item>
    </n-form>

    <template #footer>
      <n-space justify="end">
        <n-button @click="handleCancel">{{ t('common.cancel') }}</n-button>
        <n-button type="primary" :loading="saving" @click="handleSave">{{ t('common.save') }}</n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
import { reactive, ref, watch } from 'vue'
import { NModal, NForm, NFormItem, NInput, NInputNumber, NButton, NSpace, NSwitch, useMessage } from 'naive-ui'
import { useI18n } from 'vue-i18n'
import type { XaiAccount, XaiAccountInput } from '@/types/xai'

const { t } = useI18n()
const message = useMessage()

const props = withDefaults(defineProps<{
  show: boolean
  account: XaiAccount | null
  mode?: 'edit' | 'create-api-key'
}>(), {
  show: false,
  account: null,
  mode: 'edit'
})

const emit = defineEmits<{
  'update:show': [show: boolean]
  success: [payload: XaiAccountInput]
}>()

const visible = ref(false)
const saving = ref(false)
const isCreate = ref(false)
const formData = reactive({
  id: '',
  email: '',
  refreshToken: '',
  apiKey: '',
  authKind: 'oauth',
  proxyUrl: '',
  baseURL: '',
  weight: 1,
  enabled: true,
  websockets: false
})

watch(() => props.show, (newVal) => {
  visible.value = newVal
  if (newVal) resetForm()
})

watch(visible, (newVal) => {
  if (!newVal) emit('update:show', false)
})

function resetForm() {
  isCreate.value = props.mode === 'create-api-key'
  const account = props.account
  formData.id = account?.id || ''
  formData.email = account?.email || ''
  formData.refreshToken = account?.refreshToken || ''
  formData.apiKey = account?.apiKey || ''
  formData.authKind = isCreate.value ? 'api_key' : (account?.authKind || 'oauth')
  formData.proxyUrl = account?.proxyUrl || ''
  formData.baseURL = account?.baseURL || ''
  formData.weight = account?.weight || 1
  formData.enabled = account?.enabled !== false
  formData.websockets = Boolean(account?.websockets)
}

function handleCancel() {
  visible.value = false
}

async function handleSave() {
  if (isCreate.value && !formData.apiKey.trim()) {
    message.error(t('xai.apiKeyRequired'))
    return
  }
  saving.value = true
  try {
    emit('success', {
      id: formData.id || undefined,
      email: formData.email,
      refreshToken: formData.refreshToken,
      apiKey: formData.apiKey,
      authKind: formData.authKind,
      proxyUrl: formData.proxyUrl,
      baseURL: formData.baseURL,
      weight: formData.weight,
      enabled: formData.enabled,
      websockets: formData.websockets,
      status: 'valid'
    })
    visible.value = false
  } finally {
    saving.value = false
  }
}
</script>
