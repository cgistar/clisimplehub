<template>
  <n-modal
    v-model:show="visible"
    preset="card"
    title="获取账号"
    style="width: 520px"
    :mask-closable="false"
    closable
    @after-leave="resetState"
  >
    <!-- Phase: form -->
    <n-form v-if="phase === 'form'" label-placement="left" label-width="100px">
      <n-form-item label="邮箱供应商">
        <n-select
          v-model:value="formProvider"
          :options="providerOptions"
          placeholder="选择邮箱供应商"
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

      <template v-if="formProvider === 'tempmail'">
        <n-form-item label="注册邮箱">
          <n-input v-model:value="formEmail" placeholder="用于注册 OpenAI 的真实邮箱" @blur="validateEmail('formEmail')" />
        </n-form-item>
        <n-form-item label="TempMail 邮箱">
          <n-input-group>
            <n-input v-model:value="formParams.tempmail_forward_email" placeholder="接收转发邮件的临时邮箱" />
            <n-button :loading="generating" @click="handleRandomGenerate">
              随机生成
            </n-button>
          </n-input-group>
        </n-form-item>
        <n-form-item label="PIN 码">
          <n-input v-model:value="formParams.tempmail_epin" placeholder="可选，无则留空" />
        </n-form-item>
      </template>

      <template v-if="formProvider === 'outlook'">
        <n-form-item label="账号信息">
          <n-input
            v-model:value="formParams.outlook_raw_input"
            type="textarea"
            :rows="4"
            placeholder="粘贴完整账号信息（支持多个'-'分隔）&#10;例如: email@outlook.com----password----client-id----refresh-token"
            @blur="parseOutlookInput"
          />
        </n-form-item>
        <n-form-item label="邮箱地址">
          <n-input v-model:value="formParams.outlook_email" placeholder="user@outlook.com" @blur="validateEmail('outlook_email')" />
        </n-form-item>
        <n-form-item label="模式">
          <n-radio-group v-model:value="formParams.outlook_mode">
            <n-radio value="imap">IMAP</n-radio>
            <n-radio value="graph">Graph API</n-radio>
          </n-radio-group>
        </n-form-item>
        <n-form-item label="Client ID">
          <n-input v-model:value="formParams.outlook_client_id" placeholder="Azure AD 应用 ID" />
        </n-form-item>
        <n-form-item label="Refresh Token">
          <n-input v-model:value="formParams.outlook_refresh_token" type="textarea" :rows="3" placeholder="OAuth2 刷新令牌" />
        </n-form-item>
      </template>

      <n-form-item label="邮箱" v-if="formProvider !== 'outlook' && formProvider !== 'tempmail'">
        <n-input-group>
          <n-input v-model:value="formEmail" placeholder="手动输入或点击随机生成" @blur="validateEmail('formEmail')" />
          <n-button v-if="canRandomGenerate" :loading="generating" @click="handleRandomGenerate">
            随机生成
          </n-button>
        </n-input-group>
      </n-form-item>

      <n-alert v-if="verificationCode" type="success" :bordered="false" style="margin-bottom: 16px">
        验证码：{{ verificationCode }}（已复制到剪贴板）
      </n-alert>
      <n-alert v-else-if="verificationConnected" type="info" :bordered="false" style="margin-bottom: 16px">
        邮箱已连接，最近邮件中还没有验证码
      </n-alert>

      <n-form-item label="密码">
        <n-input v-model:value="formPassword" type="password" show-password-on="click" placeholder="留空自动生成" />
      </n-form-item>

      <n-form-item label="Client ID">
        <n-input v-model:value="formClientId" placeholder="app_EMoamEEZ73f0CkXaXp7hrann" />
      </n-form-item>
    </n-form>

    <!-- Phase: signing_up / submitting_otp -->
    <div v-else-if="phase === 'signing_up' || phase === 'submitting_otp'" style="padding: 16px 0">
      <n-space vertical align="center" :size="12">
        <n-spin size="large" />
        <span>{{ phase === 'submitting_otp' ? '正在提交验证码...' : '正在注册...' }}</span>
      </n-space>
      <div v-if="stepLogs.length" class="step-log" style="margin-top: 12px">
        <div v-for="(log, i) in stepLogs" :key="i" class="step-log-line">{{ log }}</div>
      </div>
    </div>

    <!-- Phase: need_otp -->
    <div v-else-if="phase === 'need_otp'" style="padding: 16px 0">
      <n-alert type="info" style="margin-bottom: 12px">
        请输入邮箱收到的验证码
      </n-alert>
      <n-input v-model:value="otpCode" placeholder="6位验证码" maxlength="6" />
      <div v-if="stepLogs.length" class="step-log" style="margin-top: 12px">
        <div v-for="(log, i) in stepLogs" :key="i" class="step-log-line">{{ log }}</div>
      </div>
    </div>

    <!-- Phase: success -->
    <n-result v-else-if="phase === 'success'" status="success" title="注册成功" />

    <!-- Phase: error -->
    <n-result v-else-if="phase === 'error'" status="error" title="注册失败" :description="errorMsg" />

    <template #footer>
      <n-space v-if="phase === 'form'" justify="end">
        <n-button @click="handleClose">取消</n-button>
        <n-button
          v-if="canFetchVerificationCode || fetchingVerificationCode"
          :loading="fetchingVerificationCode"
          :disabled="!canFetchVerificationCode"
          @click="handleFetchVerificationCode"
        >
          验证码
        </n-button>
        <n-button type="primary" :disabled="!canSubmit" @click="handleStart">开始注册</n-button>
      </n-space>
      <n-space v-else-if="phase === 'need_otp'" justify="end">
        <n-button @click="handleCancel">取消</n-button>
        <n-button type="primary" :disabled="otpCode.length < 6" @click="handleSubmitOTP">提交验证码</n-button>
      </n-space>
      <n-space v-else-if="phase === 'error'" justify="end">
        <n-button @click="handleClose">关闭</n-button>
        <n-button type="primary" @click="resetToForm">重试</n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
import { ref, computed, watch, onUnmounted, nextTick, reactive } from 'vue'
import { NModal, NForm, NFormItem, NSelect, NInput, NInputGroup, NRadio, NRadioGroup, NButton, NSpace, NSpin, NAlert, NResult, useMessage } from 'naive-ui'
import { codexApi } from '../../api/codex'
import { useCodexAccountsStore } from '../../stores/codexAccountsStore'
import type { CodexAccountInput, CodexSignupRequest, SignupState } from '@/types/codex'

type Phase = 'form' | 'signing_up' | 'need_otp' | 'submitting_otp' | 'success' | 'error'

const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{
  'update:show': [value: boolean]
  'success': [account: CodexAccountInput]
}>()

const message = useMessage()
const codexStore = useCodexAccountsStore()
const visible = ref(false)
const phase = ref<Phase>('form')

const formProvider = ref('')
const formParams = reactive<Record<string, string>>({
  duckmail_api_base: '',
  gptmail_api_base: '',
  gptmail_api_key: '',
  cf_worker_domain: '',
  cf_email_domain: '',
  cf_admin_password: '',
  tempmail_forward_email: '',
  tempmail_epin: '',
  outlook_raw_input: '',
  outlook_email: '',
  outlook_mode: 'imap',
  outlook_client_id: '',
  outlook_refresh_token: ''
})
const formEmail = ref('')
const formPassword = ref('')
const formClientId = ref('app_EMoamEEZ73f0CkXaXp7hrann')

const otpCode = ref('')
const errorMsg = ref('')
const stepLogs = ref<string[]>([])
const signupResult = ref<SignupState | null>(null)
const providerState = ref<Record<string, string>>({})
const generating = ref(false)
const fetchingVerificationCode = ref(false)
const verificationCode = ref('')
const verificationConnected = ref(false)
const parsedOutlookRawInput = ref('')

const canRandomGenerate = computed(() =>
  ['duckmail', 'cloudflare', 'gptmail'].includes(formProvider.value)
)

const providerOptions = computed(() => [
  { label: '无（手动模式）', value: '' },
  { label: 'DuckMail', value: 'duckmail' },
  { label: 'GPTMail', value: 'gptmail' },
  { label: 'Cloudflare临时邮箱', value: 'cloudflare' },
  { label: 'TempMail', value: 'tempmail' },
  { label: 'Outlook', value: 'outlook' }
])

const canSubmit = computed(() => {
  if (formProvider.value === 'outlook') {
    return canFetchVerificationCode.value
  }
  if (formProvider.value === 'tempmail') {
    if (!formEmail.value || !isValidEmail(formEmail.value)) return false
    if (!formParams.tempmail_forward_email || !isValidEmail(formParams.tempmail_forward_email)) return false
    return true
  }
  // 所有非 outlook/tempmail 供应商都需要邮箱
  if (!formEmail.value || !isValidEmail(formEmail.value)) return false
  if (formProvider.value === 'cloudflare') {
    if (!formParams.cf_worker_domain || !formParams.cf_email_domain || !formParams.cf_admin_password) return false
  }
  return true
})

const canFetchVerificationCode = computed(() => {
  switch (formProvider.value) {
    case 'outlook':
      if (!formParams.outlook_email || !formParams.outlook_client_id || !formParams.outlook_refresh_token) return false
      return isValidEmail(formParams.outlook_email)
    case 'duckmail':
      return Boolean(formEmail.value && isValidEmail(formEmail.value) && providerState.value._mail_token)
    case 'gptmail':
      return Boolean(formEmail.value && isValidEmail(formEmail.value))
    case 'cloudflare':
      return Boolean(
        formParams.cf_worker_domain &&
        (providerState.value._jwt || formParams.cf_admin_password)
      )
    case 'tempmail':
      return Boolean(formParams.tempmail_forward_email && isValidEmail(formParams.tempmail_forward_email))
    default:
      return false
  }
})

// --- Event listening ---
let offStepEvent: (() => void) | null = null

function startListening() {
  stopListening()
  if ((window as any).runtime?.EventsOn) {
    offStepEvent = (window as any).runtime.EventsOn('codex:signup-step', (msg: string) => {
      stepLogs.value.push(msg)
      nextTick(() => {
        const el = document.querySelector('.step-log')
        if (el) el.scrollTop = el.scrollHeight
      })
    })
  }
}

function stopListening() {
  if (offStepEvent) { offStepEvent(); offStepEvent = null }
}

onUnmounted(stopListening)

// --- Email validation ---
function isValidEmail(email: string): boolean {
  const emailRegex = /^[^\s@]+@[^\s@]+\.[^\s@]+$/
  return emailRegex.test(email)
}

function validateEmail(field: 'formEmail' | 'outlook_email') {
  const email = field === 'formEmail' ? formEmail.value : formParams.outlook_email
  if (email && !isValidEmail(email)) {
    message.warning('邮箱格式不正确')
  }
}

// --- Watchers ---
watch(() => props.show, (v) => {
  visible.value = v
  if (v) resetToForm()
})
watch(visible, (v) => {
  if (!v) { emit('update:show', false); stopListening() }
})
watch(
  () => [
    formProvider.value,
    formParams.outlook_raw_input,
    formParams.outlook_email,
    formParams.outlook_client_id,
    formParams.outlook_refresh_token,
    formParams.outlook_mode,
    formEmail.value,
    formParams.duckmail_api_base,
    formParams.gptmail_api_base,
    formParams.gptmail_api_key,
    formParams.cf_worker_domain,
    formParams.tempmail_forward_email,
    formParams.tempmail_epin,
    JSON.stringify(providerState.value)
  ],
  () => clearVerificationStatus()
)

// --- Actions ---
function resetToForm() {
  phase.value = 'form'
  otpCode.value = ''
  errorMsg.value = ''
  stepLogs.value = []
  signupResult.value = null
  providerState.value = {}
  clearVerificationStatus()
}

function clearVerificationStatus() {
  verificationCode.value = ''
  verificationConnected.value = false
}

function resetState() {
  // 如果正在注册过程中，取消注册
  if (phase.value === 'signing_up' || phase.value === 'submitting_otp') {
    codexApi.cancelSignup().catch(() => {})
  }
  resetToForm()
  formProvider.value = ''
  formParams.duckmail_api_base = ''
  formParams.gptmail_api_base = ''
  formParams.gptmail_api_key = ''
  formParams.cf_worker_domain = ''
  formParams.cf_email_domain = ''
  formParams.cf_admin_password = ''
  formParams.tempmail_forward_email = ''
  formParams.tempmail_epin = ''
  formParams.outlook_raw_input = ''
  formParams.outlook_email = ''
  formParams.outlook_mode = 'imap'
  formParams.outlook_client_id = ''
  formParams.outlook_refresh_token = ''
  formEmail.value = ''
  formPassword.value = ''
  formClientId.value = 'app_EMoamEEZ73f0CkXaXp7hrann'
  fetchingVerificationCode.value = false
  parsedOutlookRawInput.value = ''
}

// Parse Outlook account info from raw input
// Format: email----password----client-id----refresh-token
// Supports multiple '-' separators (e.g., ----, --, etc.)
function parseOutlookInput(): boolean {
  const raw = formParams.outlook_raw_input?.trim()
  if (!raw) {
    parsedOutlookRawInput.value = ''
    return true
  }
  if (raw === parsedOutlookRawInput.value) return true

  // Split by multiple dashes (2 or more consecutive dashes)
  const parts = raw.split(/--+/).map(p => p.trim()).filter(p => p.length > 0)

  if (parts.length < 3) {
    message.warning('Outlook 账号信息不完整，未更新邮箱字段')
    return false
  }

  // Identify each part by pattern matching
  let email = ''
  let clientId = ''
  let refreshToken = ''
  const remainingParts: string[] = []

  for (const part of parts) {
    if (!email && part.includes('@')) {
      // Email: contains @ symbol
      email = part
    } else if (!clientId && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(part)) {
      // Client ID: UUID format
      clientId = part
    } else {
      remainingParts.push(part)
    }
  }

  // Refresh token: longest remaining part
  if (remainingParts.length > 0) {
    refreshToken = remainingParts.reduce((longest, current) =>
      current.length > longest.length ? current : longest
    )
  }

  if (!email || !clientId || !refreshToken) {
    message.warning('Outlook 账号信息解析失败，请确认包含邮箱、Client ID 和 Refresh Token')
    return false
  }

  // Update form fields
  formParams.outlook_email = email
  formParams.outlook_client_id = clientId
  formParams.outlook_refresh_token = refreshToken
  parsedOutlookRawInput.value = raw
  return true
}

async function copyText(text: string): Promise<void> {
  if (typeof navigator !== 'undefined' && navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text)
    return
  }
  throw new Error('clipboard_unavailable')
}

async function handleFetchVerificationCode() {
  if (!canFetchVerificationCode.value || fetchingVerificationCode.value) return

  fetchingVerificationCode.value = true
  clearVerificationStatus()
  try {
    const params = buildVerificationParams()
    const result = await codexApi.fetchVerificationCode({
      emailProvider: formProvider.value,
      providerParams: params,
      email: verificationEmail(),
      timeoutSec: 120
    })
    const code = result.code?.trim()
    if (!code) {
      verificationConnected.value = true
      message.info('邮箱已连接，最近邮件中还没有验证码')
      return
    }

    verificationCode.value = code
    try {
      await copyText(code)
      message.success(`验证码 ${code} 已复制到剪贴板`)
    } catch (copyError) {
      message.warning(`验证码 ${code} 获取成功，但复制失败: ${copyError instanceof Error ? copyError.message : String(copyError)}`)
    }
  } catch (e) {
    message.error('获取验证码失败: ' + (e instanceof Error ? e.message : String(e)))
  } finally {
    fetchingVerificationCode.value = false
  }
}

function verificationEmail(): string {
  if (formProvider.value === 'outlook') return formParams.outlook_email
  if (formProvider.value === 'tempmail') return formEmail.value
  return formEmail.value
}

function buildVerificationParams(): Record<string, string> {
  const common = {
    ...providerState.value,
    mail_wait_for_new: 'false'
  }

  switch (formProvider.value) {
    case 'outlook':
      return {
        ...common,
        outlook_email: formParams.outlook_email,
        outlook_mode: formParams.outlook_mode,
        outlook_client_id: formParams.outlook_client_id,
        outlook_refresh_token: formParams.outlook_refresh_token,
        outlook_delete_after_fetch: 'false',
        outlook_include_existing: 'true',
        outlook_existing_max_age_sec: '0',
        outlook_existing_limit: '5',
        outlook_wait_for_new: 'false'
      }
    case 'duckmail':
      return {
        ...common,
        _email: formEmail.value,
        duckmail_api_base: formParams.duckmail_api_base || 'https://api.duckmail.sbs'
      }
    case 'gptmail':
      return {
        ...common,
        _email: formEmail.value,
        gptmail_api_base: formParams.gptmail_api_base || 'https://mail.chatgpt.org.uk',
        gptmail_api_key: formParams.gptmail_api_key || 'gpt-test'
      }
    case 'cloudflare':
      return {
        ...common,
        _email: formEmail.value,
        cf_worker_domain: formParams.cf_worker_domain,
        cf_email_domain: formParams.cf_email_domain,
        cf_admin_password: formParams.cf_admin_password
      }
    case 'tempmail':
      return {
        ...common,
        _email: formEmail.value,
        tempmail_forward_email: formParams.tempmail_forward_email,
        tempmail_epin: formParams.tempmail_epin
      }
    default:
      return common
  }
}

async function handleRandomGenerate() {
  generating.value = true
  try {
    const params = { ...formParams }
    if (formProvider.value === 'duckmail' && !params.duckmail_api_base) {
      params.duckmail_api_base = 'https://api.duckmail.sbs'
    }
    const result = await codexApi.generateRandomEmail(formProvider.value, params)

    // TempMail 特殊处理：生成的是转发邮箱
    if (formProvider.value === 'tempmail') {
      formParams.tempmail_forward_email = result.email
      providerState.value = result.providerState || {}
      message.success('TempMail 邮箱生成成功')
    } else {
      formEmail.value = result.email
      formPassword.value = result.password
      providerState.value = result.providerState || {}
      message.success('邮箱生成成功')
    }
  } catch (e) {
    message.error('生成失败: ' + (e instanceof Error ? e.message : String(e)))
  } finally {
    generating.value = false
  }
}

async function handleStart() {
  if (formProvider.value === 'outlook' && !parseOutlookInput()) return

  // 检查邮箱是否已存在
  const emailToCheck = formProvider.value === 'outlook' ? formParams.outlook_email : formEmail.value
  if (emailToCheck) {
    const existingAccount = codexStore.accounts.find(acc => acc.email === emailToCheck)
    if (existingAccount) {
      message.warning(`邮箱 ${emailToCheck} 已存在于账号列表中`)
      return
    }
  }

  phase.value = 'signing_up'
  stepLogs.value = []
  startListening()

  try {
    // Prepare provider params with default values
    const params = { ...formParams }
    if (formProvider.value === 'duckmail' && !params.duckmail_api_base) {
      params.duckmail_api_base = 'https://api.duckmail.sbs'
    }

    const req: CodexSignupRequest = {
      emailProvider: formProvider.value,
      providerParams: formProvider.value ? { ...params, ...providerState.value } : {},
      email: formProvider.value === 'outlook' ? formParams.outlook_email : formEmail.value,
      password: formPassword.value,
      clientId: formClientId.value || 'app_EMoamEEZ73f0CkXaXp7hrann'
    }
    const state = await codexApi.startSignup(req)
    handleSignupState(state)
  } catch (e) {
    errorMsg.value = e instanceof Error ? e.message : String(e)
    phase.value = 'error'
    stopListening()
  }
}

async function handleSubmitOTP() {
  phase.value = 'submitting_otp'
  try {
    const state = await codexApi.submitSignupOTP(otpCode.value)
    handleSignupState(state)
  } catch (e) {
    errorMsg.value = e instanceof Error ? e.message : String(e)
    phase.value = 'error'
    stopListening()
  }
}

function handleSignupState(state: SignupState) {
  signupResult.value = state
  if (state.error) {
    errorMsg.value = state.error
    phase.value = 'error'
    stopListening()

    // If registration succeeded but OAuth failed, show email and password for manual login
    if (state.firstStageComplete && state.result?.email && state.password) {
      errorMsg.value = `账号注册成功，获取令牌失败。\n\n邮箱: ${state.result.email}\n密码: ${state.password}\n\n${state.error}`
    }
  } else if (state.needOTP) {
    phase.value = 'need_otp'
  } else if (state.result) {
    phase.value = 'success'
    stopListening()
    const account: CodexAccountInput = {
      refreshToken: state.result.refreshToken,
      accessToken: state.result.accessToken,
      idToken: state.result.idToken,
      email: state.result.email,
      accountId: state.result.accountId,
      planType: state.result.planType,
      expiresAt: state.result.expiresAt,
      password: state.password,
      status: 'valid'
    }
    emit('success', account)
    setTimeout(() => { visible.value = false }, 1500)
  } else {
    errorMsg.value = '未知状态，请重试'
    phase.value = 'error'
    stopListening()
  }
}

async function handleCancel() {
  try { await codexApi.cancelSignup() } catch {}
  stopListening()
  resetToForm()
}

function handleClose() {
  if (phase.value === 'signing_up' || phase.value === 'submitting_otp') {
    handleCancel()
  } else {
    visible.value = false
  }
}
</script>

<style scoped>
.step-log {
  max-height: 200px;
  overflow-y: auto;
  background: var(--bg-secondary, #f5f5f5);
  border-radius: 6px;
  padding: 8px 12px;
  font-size: 12px;
  font-family: monospace;
}
.step-log-line {
  padding: 2px 0;
  color: var(--text-secondary, #666);
}
</style>
