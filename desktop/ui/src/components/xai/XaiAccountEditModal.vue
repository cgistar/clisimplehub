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
        <n-input v-model:value="formData.refreshToken" type="textarea" :rows="2" :placeholder="t('xai.refreshTokenLabel')" />
      </n-form-item>

      <n-form-item :label="t('xai.emailLabel')">
        <n-input v-model:value="formData.email" :placeholder="t('xai.emailPlaceholder')" />
      </n-form-item>

      <n-form-item :label="t('xai.proxyUrl')">
        <n-input v-model:value="formData.proxyUrl" :placeholder="t('xai.proxyUrlPlaceholder')" />
      </n-form-item>

      <n-form-item :label="t('xai.ssoLabel')">
        <n-input
          v-model:value="formData.sso"
          type="textarea"
          :rows="2"
          :placeholder="t('xai.ssoPlaceholder')"
        />
      </n-form-item>

      <n-form-item :label="t('xai.customHeadersLabel')">
        <n-input
          v-model:value="formData.customHeadersText"
          type="textarea"
          :rows="3"
          :placeholder="t('xai.customHeadersPlaceholder')"
        />
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
          <n-space align="center" justify="space-between">
            <span>{{ t('xai.usingApiLabel') }}</span>
            <n-switch v-model:value="formData.usingApi" />
          </n-space>
          <div class="using-api-help">{{ t('xai.usingApiHelp') }}</div>
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
import { NModal, NForm, NFormItem, NInput, NButton, NSpace, NSwitch, useMessage } from 'naive-ui'
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
  sso: '',
  authKind: 'oauth',
  proxyUrl: '',
  customHeadersText: '',
  weight: 1,
  enabled: true,
  websockets: true,
  usingApi: false
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
  formData.sso = account?.sso || ''
  formData.authKind = isCreate.value ? 'api_key' : (account?.authKind || 'oauth')
  formData.proxyUrl = account?.proxyUrl || ''
  formData.customHeadersText = account?.customHeaders
    ? JSON.stringify(account.customHeaders, null, 2)
    : ''
  formData.weight = account?.weight || 1
  formData.enabled = account?.enabled !== false
  // 缺省默认开启 websockets
  formData.websockets = account?.websockets !== false
  // 缺省：api_key=true 官方 API；oauth=false chat-proxy
  if (account?.usingApi !== undefined) {
    formData.usingApi = account.usingApi
  } else {
    formData.usingApi = formData.authKind === 'api_key'
  }
}

function parseCustomHeaders(text: string): Record<string, string> | undefined {
  const raw = text.trim()
  if (!raw) return undefined
  try {
    const obj = JSON.parse(raw) as Record<string, unknown>
    const out: Record<string, string> = {}
    for (const [k, v] of Object.entries(obj || {})) {
      const key = String(k || '').trim()
      const val = String(v ?? '').trim()
      if (key && val) out[key] = val
    }
    return Object.keys(out).length ? out : undefined
  } catch {
    return undefined
  }
}

function handleCancel() {
  visible.value = false
}

async function handleSave() {
  if (isCreate.value && !formData.apiKey.trim()) {
    message.error(t('xai.apiKeyRequired'))
    return
  }
  if (formData.customHeadersText.trim()) {
    const parsed = parseCustomHeaders(formData.customHeadersText)
    if (!parsed && formData.customHeadersText.trim()) {
      message.error(t('xai.customHeadersInvalid'))
      return
    }
  }
  saving.value = true
  try {
    emit('success', {
      id: formData.id || undefined,
      email: formData.email,
      refreshToken: formData.refreshToken,
      apiKey: formData.apiKey,
      sso: formData.sso,
      authKind: formData.authKind,
      proxyUrl: formData.proxyUrl,
      customHeaders: parseCustomHeaders(formData.customHeadersText),
      weight: formData.weight,
      enabled: formData.enabled,
      websockets: formData.websockets,
      usingApi: formData.usingApi,
      status: 'valid'
    })
    visible.value = false
  } finally {
    saving.value = false
  }
}
</script>

<style scoped>
.using-api-help {
  font-size: 12px;
  color: var(--text-tertiary, #94a3b8);
  line-height: 1.4;
}
</style>
