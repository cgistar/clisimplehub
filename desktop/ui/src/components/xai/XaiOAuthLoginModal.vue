<template>
  <n-modal
    v-model:show="visible"
    preset="card"
    :title="t('xai.oauthLoginModalTitle')"
    style="width: 600px"
    :mask-closable="false"
    closable
  >
    <n-space style="margin-bottom: 12px">
      <n-button size="small" :type="mode === 'browser' ? 'primary' : 'default'" @click="switchMode('browser')">
        {{ t('xai.oauthLogin') }}
      </n-button>
      <n-button size="small" :type="mode === 'device' ? 'primary' : 'default'" @click="switchMode('device')">
        {{ t('xai.deviceLogin') }}
      </n-button>
    </n-space>

    <template v-if="mode === 'browser'">
      <n-form>
        <n-form-item :label="t('xai.loginUrlLabel')">
          <n-input-group>
            <n-input :value="authUrl" readonly />
            <n-button @click="handleCopyText(authUrl)">
              <template #icon><n-icon><Copy /></n-icon></template>
            </n-button>
            <n-button @click="handleOpenUrl(authUrl, false)">
              <template #icon><n-icon><ExternalLink /></n-icon></template>
            </n-button>
            <n-button @click="handleOpenUrl(authUrl, true)">
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
    </template>

    <template v-else>
      <n-alert type="default" :bordered="false" style="margin-bottom: 12px">
        {{ t('xai.deviceLoginHeadlessHint') }}
      </n-alert>
      <n-form>
        <n-form-item :label="t('xai.deviceLoginUserCode')">
          <n-input-group>
            <n-input :value="deviceInfo?.userCode || ''" readonly />
            <n-button :disabled="!deviceInfo?.userCode" @click="handleCopyText(deviceInfo?.userCode || '')">
              <template #icon><n-icon><Copy /></n-icon></template>
            </n-button>
          </n-input-group>
        </n-form-item>
        <n-form-item v-if="deviceVerifyUrl" :label="t('xai.loginUrlLabel')">
          <n-input-group>
            <n-input :value="deviceVerifyUrl" readonly />
            <n-button :disabled="!deviceVerifyUrl" @click="handleCopyText(deviceVerifyUrl)">
              <template #icon><n-icon><Copy /></n-icon></template>
            </n-button>
            <n-button :disabled="!deviceVerifyUrl" @click="handleOpenUrl(deviceVerifyUrl, false)">
              <template #icon><n-icon><ExternalLink /></n-icon></template>
            </n-button>
            <n-button :disabled="!deviceVerifyUrl" @click="handleOpenUrl(deviceVerifyUrl, true)">
              <template #icon><n-icon><EyeOff /></n-icon></template>
            </n-button>
          </n-input-group>
        </n-form-item>
      </n-form>
    </template>

    <n-alert type="info" :bordered="false">
      <template #icon>
        <n-spin size="small" />
      </template>
      {{ mode === 'device' ? t('xai.deviceLoginWaiting') : t('xai.oauthLoginWaiting') }}
    </n-alert>

    <template #footer>
      <n-space justify="end">
        <n-button @click="handleClose">{{ t('common.close') }}</n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { NModal, NForm, NFormItem, NInput, NInputGroup, NButton, NAlert, NSpin, NIcon, NSpace, useMessage } from 'naive-ui'
import { Copy, ExternalLink, EyeOff } from 'lucide-vue-next'
import { useI18n } from 'vue-i18n'
import { xaiApi } from '@/api/xai'
import type { XaiAccountInput, XaiDeviceLoginInfo, XaiLoginResult } from '@/types/xai'

const { t } = useI18n()
const message = useMessage()

const props = withDefaults(defineProps<{ show: boolean }>(), { show: false })
const emit = defineEmits<{
  'update:show': [show: boolean]
  success: [payload: XaiAccountInput]
}>()

const visible = ref(false)
const mode = ref<'browser' | 'device'>('browser')
const authUrl = ref('')
const callbackUrl = ref('')
const deviceInfo = ref<XaiDeviceLoginInfo | null>(null)
const abortController = ref<AbortController | null>(null)
/** 会话代数：切换模式/重启登录时递增，用于丢弃过期的 wait 结果 */
const sessionGen = ref(0)

const deviceVerifyUrl = computed(() =>
  deviceInfo.value?.verificationUriComplete || deviceInfo.value?.verificationUri || ''
)

function toErrorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return String(error)
}

function isCancelledError(error: unknown): boolean {
  return toErrorMessage(error) === 'Login cancelled by user'
}

watch(() => props.show, async (newVal) => {
  visible.value = newVal
  if (newVal) {
    mode.value = 'browser'
    await startCurrentMode()
  } else {
    cancelLogin()
  }
})

watch(visible, (newVal) => {
  if (!newVal) {
    emit('update:show', false)
    cancelLogin()
  }
})

async function switchMode(next: 'browser' | 'device') {
  if (mode.value === next) return
  // 取消旧会话但不关弹窗；catch 侧通过 sessionGen 识别切换
  cancelLogin()
  mode.value = next
  if (visible.value) await startCurrentMode()
}

async function startCurrentMode() {
  if (mode.value === 'device') await startDeviceLogin()
  else await startLogin()
}

function emitLoginSuccess(result: XaiLoginResult) {
  emit('success', {
    accessToken: result.accessToken || '',
    refreshToken: result.refreshToken || '',
    idToken: result.idToken || '',
    expiresAt: result.expiresAt || '',
    email: result.email || '',
    subject: result.subject || '',
    lastRefresh: result.lastRefresh || '',
    authKind: 'oauth',
    enabled: true,
    status: 'valid'
  })
  visible.value = false
}

async function startLogin() {
  const myGen = ++sessionGen.value
  try {
    abortController.value = new AbortController()
    callbackUrl.value = ''
    deviceInfo.value = null
    authUrl.value = await xaiApi.startLoginWithURL()
    if (myGen !== sessionGen.value) return

    const waitPromise = xaiApi.waitForLoginCallback()
    const abortPromise = new Promise<never>((_, reject) => {
      abortController.value?.signal.addEventListener('abort', () => {
        reject(new Error('Login cancelled by user'))
      })
    })

    const result: XaiLoginResult = await Promise.race([waitPromise, abortPromise])
    if (myGen !== sessionGen.value) return
    if (!result?.accessToken && !result?.refreshToken) {
      throw new Error('No token payload received')
    }
    emitLoginSuccess(result)
  } catch (error) {
    if (myGen !== sessionGen.value) return
    if (isCancelledError(error)) {
      // 仅用户主动关闭弹窗时才会关窗（switchMode 已先递增 gen）
      if (!visible.value) return
      // 用户点关闭：visible 已 false，此处无需再关
      return
    }
    message.error(t('xai.oauthLoginFailed') + toErrorMessage(error))
  }
}

async function startDeviceLogin() {
  const myGen = ++sessionGen.value
  try {
    abortController.value = new AbortController()
    authUrl.value = ''
    callbackUrl.value = ''
    deviceInfo.value = await xaiApi.startDeviceLogin()
    if (myGen !== sessionGen.value) return

    const waitPromise = xaiApi.waitForDeviceLogin()
    const abortPromise = new Promise<never>((_, reject) => {
      abortController.value?.signal.addEventListener('abort', () => {
        reject(new Error('Login cancelled by user'))
      })
    })

    const result: XaiLoginResult = await Promise.race([waitPromise, abortPromise])
    if (myGen !== sessionGen.value) return
    if (!result?.accessToken && !result?.refreshToken) {
      throw new Error('No token payload received')
    }
    emitLoginSuccess(result)
  } catch (error) {
    if (myGen !== sessionGen.value) return
    if (isCancelledError(error)) return
    message.error(t('xai.oauthLoginFailed') + toErrorMessage(error))
  }
}

async function handleCopyText(text: string) {
  const value = String(text || '').trim()
  if (!value) return
  try {
    await navigator.clipboard.writeText(value)
    message.success(t('xai.linkCopied'))
  } catch (error) {
    message.error(toErrorMessage(error))
  }
}

async function handleOpenUrl(url: string, incognito: boolean) {
  const target = String(url || '').trim()
  if (!target) return
  try {
    if (incognito) await xaiApi.openURLInIncognito(target)
    else await xaiApi.openLoginURL(target)
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
  // 先递增 gen，使进行中的 catch 视为过期会话
  sessionGen.value += 1
  abortController.value?.abort()
  abortController.value = null
  void xaiApi.cancelLogin()
}

function handleClose() {
  visible.value = false
}
</script>
