<template>
  <n-modal
    v-model:show="visible"
    preset="card"
    :title="t('codex.getTokenTitle')"
    style="width: 480px"
    :mask-closable="false"
    closable
  >
    <!-- Phase: form -->
    <n-form v-if="phase === 'form'" label-placement="left" label-width="auto">
      <n-form-item label="Email">
        <n-input :value="account?.email || ''" readonly />
      </n-form-item>
      <n-form-item :label="t('codex.passwordLabel')">
        <n-input
          v-model:value="formPassword"
          type="password"
          show-password-on="click"
          :placeholder="t('codex.passwordPlaceholder')"
        />
      </n-form-item>

      <n-form-item label="邮箱供应商">
        <n-select
          v-model:value="formProvider"
          :options="providerOptions"
          placeholder="选择邮箱供应商（可选）"
        />
      </n-form-item>

      <template v-if="formProvider === 'duckmail'">
        <n-form-item label="API Base">
          <n-input v-model:value="formParams.duckmail_api_base" placeholder="https://api.duckmail.sbs" />
        </n-form-item>
      </template>

      <template v-if="formProvider === 'gptmail'">
        <n-form-item label="API Base">
          <n-input v-model:value="formParams.gptmail_api_base" placeholder="https://mail.chatgpt.org.uk" />
        </n-form-item>
        <n-form-item label="API Key">
          <n-input v-model:value="formParams.gptmail_api_key" type="password" show-password-on="click" placeholder="gpt-test" />
        </n-form-item>
      </template>

      <template v-if="formProvider === 'cloudflare'">
        <n-form-item label="Worker Domain">
          <n-input v-model:value="formParams.cf_worker_domain" placeholder="mail.example.com" />
        </n-form-item>
        <n-form-item label="Email Domain">
          <n-input v-model:value="formParams.cf_email_domain" placeholder="example.com" />
        </n-form-item>
        <n-form-item label="Admin Password">
          <n-input v-model:value="formParams.cf_admin_password" type="password" show-password-on="click" placeholder="管理员密码" />
        </n-form-item>
      </template>

      <n-form-item :label="t('codex.clientIdLabel')">
        <n-input
          v-model:value="formClientId"
          :placeholder="t('codex.clientIdPlaceholder')"
        />
      </n-form-item>
    </n-form>

    <!-- Phase: logging_in / submitting_otp — show spinner + step log -->
    <div v-else-if="phase === 'logging_in' || phase === 'submitting_otp'" style="padding: 16px 0">
      <n-space vertical align="center" style="margin-bottom: 12px">
        <n-spin size="large" />
        <span>{{ phase === 'logging_in' ? t('codex.headlessLoginProgress') : t('codex.otpSubmitting') }}</span>
      </n-space>
      <div v-if="stepLogs.length > 0" class="step-log">
        <div v-for="(log, i) in stepLogs" :key="i" class="step-log-line">{{ log }}</div>
      </div>
    </div>

    <!-- Phase: need_otp -->
    <div v-else-if="phase === 'need_otp'">
      <n-alert type="info" :bordered="false" style="margin-bottom: 16px">
        {{ t('codex.otpRequired') }}
      </n-alert>
      <n-form label-placement="left" label-width="auto">
        <n-form-item :label="t('codex.otpLabel')">
          <n-input
            v-model:value="otpCode"
            :placeholder="t('codex.otpPlaceholder')"
            @keyup.enter="handleSubmitOTP"
          />
        </n-form-item>
      </n-form>
    </div>

    <!-- Phase: success -->
    <n-result v-else-if="phase === 'success'" status="success" :title="t('codex.getTokenSuccess')" />

    <!-- Phase: error -->
    <n-result v-else-if="phase === 'error'" status="error" :title="t('codex.getTokenFailed')" :description="errorMsg" />

    <template #footer>
      <n-space v-if="phase === 'form'" justify="end">
        <n-button @click="handleClose">{{ t('common.cancel') }}</n-button>
        <n-button type="primary" :disabled="!formPassword" @click="handleLogin">
          {{ t('codex.loginButton') }}
        </n-button>
      </n-space>
      <n-space v-else-if="phase === 'need_otp'" justify="end">
        <n-button @click="handleClose">{{ t('common.cancel') }}</n-button>
        <n-button type="primary" :disabled="!otpCode" @click="handleSubmitOTP">
          {{ t('codex.submitOTP') }}
        </n-button>
      </n-space>
      <n-space v-else-if="phase === 'error'" justify="end">
        <n-button @click="handleClose">{{ t('common.close') }}</n-button>
        <n-button type="primary" @click="resetToForm">{{ t('codex.retry') }}</n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
import { ref, watch, onUnmounted, computed, reactive } from 'vue'
import { NModal, NForm, NFormItem, NInput, NButton, NAlert, NSpin, NSpace, NResult, NSelect, useMessage } from 'naive-ui'
import { useI18n } from 'vue-i18n'
import { codexApi } from '@/api/codex'
import type { CodexAccount, CodexAccountInput } from '@/types/codex'

const { t } = useI18n()
const message = useMessage()

type Phase = 'form' | 'logging_in' | 'need_otp' | 'submitting_otp' | 'success' | 'error'

const props = withDefaults(defineProps<{
  show: boolean
  account: CodexAccount | null
}>(), {
  show: false,
  account: null
})

const emit = defineEmits<{
  'update:show': [show: boolean]
  success: [payload: CodexAccountInput]
  'status-update': [payload: CodexAccountInput]
}>()

const visible = ref(false)
const phase = ref<Phase>('form')
const formPassword = ref('')
const formClientId = ref('')
const formProvider = ref('')
const formParams = reactive<Record<string, string>>({
  duckmail_api_base: '',
  gptmail_api_base: '',
  gptmail_api_key: '',
  cf_worker_domain: '',
  cf_email_domain: '',
  cf_admin_password: ''
})
const otpCode = ref('')
const errorMsg = ref('')
const stepLogs = ref<string[]>([])

const providerOptions = computed(() => [
  { label: '无（手动模式）', value: '' },
  { label: 'DuckMail', value: 'duckmail' },
  { label: 'GPTMail', value: 'gptmail' },
  { label: 'Cloudflare临时邮箱', value: 'cloudflare' }
])

// Listen for step progress events from backend
let offStepEvent: (() => void) | null = null

function startListening() {
  if (offStepEvent) return
  if (window.runtime?.EventsOn) {
    offStepEvent = window.runtime.EventsOn('codex:headless-step', (msg: string) => {
      stepLogs.value.push(msg)
    }) as (() => void) | null
  }
}

function stopListening() {
  if (offStepEvent) {
    offStepEvent()
    offStepEvent = null
  }
}

onUnmounted(stopListening)

function toErrorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return String(error)
}

function isAccountDeactivatedError(message: string): boolean {
  const normalized = message.toLowerCase()
  return normalized.includes('account_deactivated') ||
    normalized.includes('deleted or deactivated')
}

function emitBannedStatusIfDeactivated(rawError: unknown): void {
  const accountId = props.account?.accountId?.trim()
  if (!accountId) return

  const message = toErrorMessage(rawError)
  if (!isAccountDeactivatedError(message)) return

  emit('status-update', {
    accountId,
    status: 'banned'
  })
}

watch(() => props.show, (newVal) => {
  visible.value = newVal
  if (newVal) {
    resetToForm()
    formPassword.value = props.account?.password || ''
  }
})

watch(visible, (newVal) => {
  if (!newVal) {
    emit('update:show', false)
    stopListening()
    void codexApi.cancelHeadlessLogin().catch(() => {})
  }
})

function resetToForm() {
  phase.value = 'form'
  otpCode.value = ''
  errorMsg.value = ''
  stepLogs.value = []
  formProvider.value = ''
  formParams.duckmail_api_base = ''
  formParams.gptmail_api_base = ''
  formParams.gptmail_api_key = ''
  formParams.cf_worker_domain = ''
  formParams.cf_email_domain = ''
  formParams.cf_admin_password = ''
}

async function handleLogin() {
  const email = props.account?.email || ''
  if (!email || !formPassword.value) return

  phase.value = 'logging_in'
  stepLogs.value = []
  startListening()

  try {
    let state
    if (formProvider.value) {
      // Prepare provider params with default values
      const params = { ...formParams }
      if (formProvider.value === 'duckmail' && !params.duckmail_api_base) {
        params.duckmail_api_base = 'https://api.duckmail.sbs'
      }

      // Use provider mode
      state = await codexApi.startHeadlessLoginWithProvider(
        email,
        formPassword.value,
        formClientId.value || 'app_EMoamEEZ73f0CkXaXp7hrann',
        formProvider.value,
        params
      )
    } else {
      // Manual mode
      state = await codexApi.startHeadlessLogin(
        email,
        formPassword.value,
        formClientId.value || 'app_EMoamEEZ73f0CkXaXp7hrann'
      )
    }

    if (state.error) {
      emitBannedStatusIfDeactivated(state.error)
      phase.value = 'error'
      errorMsg.value = state.error
      return
    }

    if (state.needOTP) {
      phase.value = 'need_otp'
      return
    }

    if (state.result) {
      handleSuccess(state.result)
      return
    }

    phase.value = 'error'
    errorMsg.value = 'Unexpected state'
  } catch (error) {
    emitBannedStatusIfDeactivated(error)
    phase.value = 'error'
    errorMsg.value = toErrorMessage(error)
  }
}

async function handleSubmitOTP() {
  if (!otpCode.value) return

  phase.value = 'submitting_otp'
  stepLogs.value = []
  startListening()

  try {
    const state = await codexApi.submitHeadlessOTP(otpCode.value)

    if (state.error) {
      emitBannedStatusIfDeactivated(state.error)
      phase.value = 'error'
      errorMsg.value = state.error
      return
    }

    if (state.result) {
      handleSuccess(state.result)
      return
    }

    phase.value = 'error'
    errorMsg.value = 'Unexpected state'
  } catch (error) {
    emitBannedStatusIfDeactivated(error)
    phase.value = 'error'
    errorMsg.value = toErrorMessage(error)
  }
}

function handleSuccess(result: any) {
  phase.value = 'success'
  stopListening()
  emit('success', {
    refreshToken: result.refreshToken || '',
    accessToken: result.accessToken || '',
    idToken: result.idToken || '',
    expiresAt: result.expiresAt || '',
    email: result.email || props.account?.email || '',
    accountId: result.accountId || props.account?.accountId || '',
    planType: result.planType || '',
    status: 'valid',
    password: formPassword.value
  })

  setTimeout(() => {
    visible.value = false
  }, 1500)
}

function handleClose() {
  visible.value = false
}
</script>

<style scoped>
.step-log {
  background: var(--bg-tertiary, #f5f5f5);
  border-radius: 4px;
  padding: 8px 12px;
  max-height: 120px;
  overflow-y: auto;
  font-family: monospace;
  font-size: 11px;
  line-height: 1.6;
  color: var(--text-secondary, #666);
}
.step-log-line {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
</style>
