<template>
  <n-modal
    v-model:show="visible"
    preset="card"
    :title="t('codex.oauthLoginModalTitle')"
    style="width: 600px"
    :mask-closable="false"
    closable
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
    </n-form>

    <n-alert type="info" :bordered="false">
      <template #icon>
        <n-spin size="small" />
      </template>
      {{ t('codex.oauthLoginWaiting') }}
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
const abortController = ref<AbortController | null>(null)

function toErrorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return String(error)
}

watch(() => props.show, async (newVal) => {
  visible.value = newVal
  if (newVal) {
    await startLogin()
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

async function startLogin() {
  try {
    abortController.value = new AbortController()

    // Get OAuth URL
    authUrl.value = await codexApi.startLoginWithURL()

    // Wait for callback
    const waitPromise = codexApi.waitForLoginCallback()
    const abortPromise = new Promise<never>((_, reject) => {
      abortController.value?.signal.addEventListener('abort', () => {
        reject(new Error('Login cancelled by user'))
      })
    })

    const result: CodexLoginResult = await Promise.race([waitPromise, abortPromise])

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
    const errMsg = toErrorMessage(error)
    if (errMsg === 'Login cancelled by user') {
      visible.value = false
      return
    }

    // Keep modal open on startup failures so users can still close/retry explicitly.
    message.error(t('codex.oauthLoginFailed') + ': ' + errMsg)
  }
}

function cancelLogin() {
  void codexApi.cancelLogin().catch(() => {
    // Ignore cleanup errors when no login session exists.
  })

  if (abortController.value) {
    abortController.value.abort()
    abortController.value = null
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
