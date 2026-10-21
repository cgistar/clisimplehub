<template>
  <n-modal
    v-model:show="visible"
    preset="card"
    :title="t('codex.oauthLoginModalTitle')"
    style="width: 600px"
    :mask-closable="false"
    :closable="!submitting"
  >
    <n-form>
      <n-form-item :label="t('codex.loginUrlLabel')">
        <n-input-group>
          <n-input
            :value="authUrl"
            readonly
            @click="handleSelectUrl"
          />
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

      <n-form-item :label="t('codex.callbackUrlLabel')">
        <n-input-group>
          <n-input
            v-model:value="callbackUrl"
            :placeholder="t('codex.callbackUrlPlaceholder')"
            :disabled="submitting"
            clearable
          />
          <n-button :disabled="!callbackUrl.trim() || starting || submitting || !active" :loading="submitting" @click="handleSubmitCallbackUrl">
            {{ t('codex.submitCallbackUrl') }}
          </n-button>
        </n-input-group>
      </n-form-item>
    </n-form>

    <n-alert type="info" :bordered="false">
      <template #icon>
        <n-spin size="small" />
      </template>
      {{ t('codex.oauthLoginWaiting') }}
    </n-alert>

    <template #footer>
      <n-space justify="end">
        <n-button :disabled="submitting" @click="handleClose">{{ t('common.close') }}</n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { NModal, NForm, NFormItem, NInput, NInputGroup, NButton, NAlert, NSpin, NIcon, NSpace, useMessage } from 'naive-ui'
import { Copy, ExternalLink, EyeOff } from 'lucide-vue-next'
import { useI18n } from 'vue-i18n'
import { codexApi } from '@/api/codex'
import type { CodexAccountInput, CodexLoginResult } from '@/types/codex'

const { t } = useI18n()
const message = useMessage()

const props = withDefaults(defineProps<{
  show: boolean
}>(), {
  show: false
})

const emit = defineEmits<{
  'update:show': [show: boolean]
  success: [payload: CodexAccountInput]
}>()

const visible = ref(false)
const authUrl = ref('')
const callbackUrl = ref('')
const active = ref(false)
const starting = ref(false)
const submitting = ref(false)
let generation = 0
// 串行执行启停，防止快速重开时旧关闭请求停止新监听。
let lifecycle: Promise<unknown> = Promise.resolve()

function toErrorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return String(error)
}

watch(() => props.show, (newVal) => {
  visible.value = newVal
  if (newVal) void startLogin()
  else void cancelLogin().catch((error) => message.error(toErrorMessage(error)))
})

watch(visible, (newVal) => {
  if (!newVal) emit('update:show', false)
})

onBeforeUnmount(() => { void cancelLogin().catch((error) => message.error(toErrorMessage(error))) })

async function startLogin() {
  const gen = ++generation
  active.value = true
  starting.value = true
  callbackUrl.value = ''
  authUrl.value = ''
  try {
    const pending = lifecycle.then(() => codexApi.startLoginWithURL())
    lifecycle = pending.then(() => undefined, () => undefined)
    const url = await pending
    if (gen !== generation) return
    authUrl.value = url
    starting.value = false
    const result: CodexLoginResult = await codexApi.waitForLoginCallback()
    if (gen !== generation) return

    if (!result?.accountId) throw new Error('No account ID received')
    if (!result?.refreshToken && !result?.accessToken) throw new Error('No token payload received')

    emit('success', {
      refreshToken: result.refreshToken || '',
      accessToken: result.accessToken || '',
      idToken: result.idToken || '',
      expiresAt: result.expiresAt || '',
      email: result.email || '',
      accountId: result.accountId,
      planType: result.planType || '',
      status: 'valid'
    })

    visible.value = false
  } catch (error) {
    if (gen === generation) message.error(t('codex.oauthLoginFailed') + toErrorMessage(error))
  } finally {
    if (gen === generation) {
      active.value = false
      starting.value = false
      authUrl.value = ''
    }
  }
}

async function handleSubmitCallbackUrl() {
  if (submitting.value) return
  submitting.value = true
  try {
    await codexApi.submitLoginCallbackURL(callbackUrl.value)
  } catch (error) {
    message.error(t('codex.oauthCallbackSubmitFailed') + ': ' + toErrorMessage(error))
  } finally {
    submitting.value = false
  }
}

async function cancelLogin() {
  const gen = ++generation
  const pending = lifecycle.then(() => codexApi.cancelLogin())
  lifecycle = pending.then(() => undefined, () => undefined)
  await pending
  if (gen === generation) {
    active.value = false
    starting.value = false
    authUrl.value = ''
    callbackUrl.value = ''
  }
}

function handleSelectUrl(event: Event): void {
  const target = event.target as HTMLInputElement | null
  target?.select()
}

async function handleCopyUrl() {
  try {
    await navigator.clipboard.writeText(authUrl.value)
    message.success(t('codex.linkCopied'))
  } catch (error) {
    message.error(t('codex.copyFailed') + ': ' + toErrorMessage(error))
  }
}

async function handleOpenUrl(incognito = false) {
  try {
    if (incognito) {
      await codexApi.openURLInIncognito(authUrl.value)
    } else {
      if (window.runtime?.BrowserOpenURL) {
        window.runtime.BrowserOpenURL(authUrl.value)
      } else {
        window.open(authUrl.value, '_blank', 'noopener,noreferrer')
      }
    }
  } catch (error) {
    message.error(t('codex.openLinkFailed') + ': ' + toErrorMessage(error))
  }
}

function handleClose(): void {
  visible.value = false
}

</script>
