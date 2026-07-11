<template>
  <n-modal
    v-model:show="visible"
    preset="card"
    :title="t('xai.oauthLoginModalTitle')"
    style="width: 600px"
    :mask-closable="false"
    closable
  >
    <n-form>
      <n-form-item :label="t('xai.loginUrlLabel')">
        <n-input-group>
          <n-input :value="authUrl" readonly />
          <n-button @click="handleCopyUrl">
            <template #icon><n-icon><Copy /></n-icon></template>
          </n-button>
          <n-button @click="handleOpenUrl(false)">
            <template #icon><n-icon><ExternalLink /></n-icon></template>
          </n-button>
          <n-button @click="handleOpenUrl(true)">
            <template #icon><n-icon><EyeOff /></n-icon></template>
          </n-button>
        </n-input-group>
      </n-form-item>

      <n-form-item :label="t('xai.callbackUrlLabel')">
        <n-input-group>
          <n-input
            v-model:value="callbackUrl"
            :placeholder="t('xai.callbackUrlPlaceholder')"
            clearable
          />
          <n-button :disabled="!callbackUrl.trim()" @click="handleSubmitCallbackUrl">
            {{ t('xai.submitCallbackUrl') }}
          </n-button>
        </n-input-group>
      </n-form-item>
    </n-form>

    <n-alert type="info" :bordered="false">
      <template #icon>
        <n-spin size="small" />
      </template>
      {{ t('xai.oauthLoginWaiting') }}
    </n-alert>

    <template #footer>
      <n-space justify="end">
        <n-button @click="handleClose">{{ t('common.close') }}</n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { NModal, NForm, NFormItem, NInput, NInputGroup, NButton, NAlert, NSpin, NIcon, NSpace, useMessage } from 'naive-ui'
import { Copy, ExternalLink, EyeOff } from 'lucide-vue-next'
import { useI18n } from 'vue-i18n'
import { xaiApi } from '@/api/xai'
import type { XaiAccountInput, XaiLoginResult } from '@/types/xai'

const { t } = useI18n()
const message = useMessage()

const props = withDefaults(defineProps<{ show: boolean }>(), { show: false })
const emit = defineEmits<{
  'update:show': [show: boolean]
  success: [payload: XaiAccountInput]
}>()

const visible = ref(false)
const authUrl = ref('')
const callbackUrl = ref('')
const abortController = ref<AbortController | null>(null)

function toErrorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return String(error)
}

watch(() => props.show, async (newVal) => {
  visible.value = newVal
  if (newVal) await startLogin()
  else cancelLogin()
})

watch(visible, (newVal) => {
  if (!newVal) {
    emit('update:show', false)
    cancelLogin()
  }
})

async function startLogin() {
  try {
    abortController.value = new AbortController()
    callbackUrl.value = ''
    authUrl.value = await xaiApi.startLoginWithURL()

    const waitPromise = xaiApi.waitForLoginCallback()
    const abortPromise = new Promise<never>((_, reject) => {
      abortController.value?.signal.addEventListener('abort', () => {
        reject(new Error('Login cancelled by user'))
      })
    })

    const result: XaiLoginResult = await Promise.race([waitPromise, abortPromise])
    if (!result?.accessToken && !result?.refreshToken) {
      throw new Error('No token payload received')
    }

    emit('success', {
      accessToken: result.accessToken || '',
      refreshToken: result.refreshToken || '',
      idToken: result.idToken || '',
      expiresAt: result.expiresAt || '',
      email: result.email || '',
      subject: result.subject || '',
      baseURL: result.baseURL || '',
      redirectURI: result.redirectURI || '',
      tokenEndpoint: result.tokenEndpoint || '',
      lastRefresh: result.lastRefresh || '',
      authKind: 'oauth',
      enabled: true,
      status: 'valid'
    })
    visible.value = false
  } catch (error) {
    const errMsg = toErrorMessage(error)
    if (errMsg === 'Login cancelled by user') {
      visible.value = false
      return
    }
    message.error(t('xai.oauthLoginFailed') + errMsg)
  }
}

async function handleCopyUrl() {
  try {
    await navigator.clipboard.writeText(authUrl.value)
    message.success(t('xai.linkCopied'))
  } catch (error) {
    message.error(toErrorMessage(error))
  }
}

async function handleOpenUrl(incognito: boolean) {
  try {
    if (incognito) await xaiApi.openURLInIncognito(authUrl.value)
    else await xaiApi.openLoginURL(authUrl.value)
  } catch (error) {
    message.error(t('xai.openLinkFailed') + ': ' + toErrorMessage(error))
  }
}

async function handleSubmitCallbackUrl() {
  try {
    await xaiApi.submitLoginCallbackURL(callbackUrl.value.trim())
  } catch (error) {
    message.error(toErrorMessage(error))
  }
}

function cancelLogin() {
  abortController.value?.abort()
  abortController.value = null
  void xaiApi.cancelLogin()
}

function handleClose() {
  visible.value = false
}
</script>
