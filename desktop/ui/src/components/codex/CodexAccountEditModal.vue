<template>
  <n-modal
    v-model:show="visible"
    preset="card"
    :title="t('codex.editAccountModalTitle')"
    style="width: 500px"
  >
    <n-form ref="formRef" :model="formData" :rules="rules">
      <n-form-item :label="t('codex.refreshTokenLabel')" path="refreshToken">
        <n-input
          v-model:value="formData.refreshToken"
          type="textarea"
          :rows="3"
          readonly
          disabled
        />
      </n-form-item>

      <n-form-item :label="t('codex.passwordLabel')" path="password">
        <n-input
          v-model:value="formData.password"
          type="password"
          :placeholder="t('codex.passwordPlaceholder')"
          show-password-on="click"
        />
      </n-form-item>

      <n-form-item :label="t('codex.mfaCodeLabel')" path="mfaCode">
        <n-input
          v-model:value="formData.mfaCode"
          :placeholder="t('codex.mfaCodePlaceholder')"
        >
          <template #suffix>
            <span class="mfa-code-suffix">{{ totpValue }}</span>
          </template>
        </n-input>
      </n-form-item>

      <n-form-item :label="t('codex.proxyUrlLabel')" path="proxyUrl">
        <n-input
          v-model:value="formData.proxyUrl"
          :placeholder="t('codex.proxyUrlPlaceholder')"
        />
      </n-form-item>

      <n-form-item :label="t('codex.weightLabel')" path="weight">
        <n-input-number
          v-model:value="formData.weight"
          :min="0"
          :max="100"
          style="width: 100%"
        />
      </n-form-item>

      <n-form-item :label="t('codex.accountCapabilityLabel')">
        <n-space vertical>
          <n-space align="center" justify="space-between">
            <span>{{ t('codex.enabledLabel') }}</span>
            <n-switch v-model:value="formData.enabled" />
          </n-space>
          <n-space align="center" justify="space-between">
            <span>{{ t('codex.websocketsLabel') }}</span>
            <n-switch v-model:value="formData.websockets" />
          </n-space>
        </n-space>
      </n-form-item>
    </n-form>

    <template #footer>
      <n-space justify="end">
        <n-button v-if="canRestore" :loading="props.restoring" @click="handleRestore">
          {{ t('codex.restoreAccountStatus') }}
        </n-button>
        <n-button @click="handleCancel">{{ t('common.cancel') }}</n-button>
        <n-button type="primary" @click="handleSave">{{ t('common.save') }}</n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import type { FormInst, FormRules } from 'naive-ui'
import { NModal, NForm, NFormItem, NInput, NInputNumber, NButton, NSpace, NSwitch } from 'naive-ui'
import { useI18n } from 'vue-i18n'
import type { CodexAccount, CodexAccountInput } from '@/types/codex'
import { generateTotpCode } from '@/utils/totp'

const { t } = useI18n()

type EditFormData = CodexAccountInput & {
  id: string
  accountId: string
  refreshToken: string
  password: string
  mfaCode: string
  proxyUrl: string
  weight: number
  enabled: boolean
  websockets: boolean
}

const props = withDefaults(defineProps<{
  show: boolean
  account: CodexAccount | null
  restoring?: boolean
}>(), {
  show: false,
  account: null,
  restoring: false
})

const emit = defineEmits<{
  'update:show': [show: boolean]
  success: [payload: CodexAccountInput]
  restore: [accountId: string]
}>()

const visible = ref(false)
const formRef = ref<FormInst | null>(null)
const totpValue = ref('')
const formData = ref<EditFormData>({
  id: '',
  accountId: '',
  refreshToken: '',
  password: '',
  mfaCode: '',
  proxyUrl: '',
  weight: 0,
  enabled: true,
  websockets: true
})

const rules: FormRules = {}
let totpTimer: ReturnType<typeof setInterval> | null = null

const canRestore = computed(() => {
  const account = props.account
  if (!account?.id) return false
  return account.status !== 'valid' || Number(account.cooldownRemaining || 0) > 0
})

async function refreshTotpValue() {
  const secret = String(formData.value.mfaCode || '').trim()
  if (!visible.value || !secret) {
    totpValue.value = ''
    return
  }
  totpValue.value = await generateTotpCode(secret)
}

function stopTotpTimer() {
  if (totpTimer) {
    clearInterval(totpTimer)
    totpTimer = null
  }
}

function startTotpTimer() {
  stopTotpTimer()
  void refreshTotpValue()
  totpTimer = setInterval(() => {
    void refreshTotpValue()
  }, 1000)
}

watch(() => props.show, (newVal) => {
  visible.value = newVal
  if (newVal && props.account) {
    formData.value = {
      id: props.account.id || '',
      accountId: props.account.accountId || '',
      refreshToken: props.account.refreshToken || '',
      password: props.account.password || '',
      mfaCode: props.account.mfaCode || '',
      proxyUrl: props.account.proxyUrl || '',
      weight: props.account.weight || 0,
      enabled: props.account.enabled !== false,
      websockets: Boolean(props.account.websockets)
    }
  }
  if (newVal) {
    startTotpTimer()
  } else {
    stopTotpTimer()
    totpValue.value = ''
  }
})

watch(visible, (newVal) => {
  if (!newVal) {
    emit('update:show', false)
  }
})

watch(() => formData.value.mfaCode, () => {
  void refreshTotpValue()
})

onBeforeUnmount(() => {
  stopTotpTimer()
})

function handleCancel() {
  visible.value = false
}

function handleRestore() {
  if (!props.account?.id || props.restoring) return
  emit('restore', props.account.id)
}

async function handleSave() {
  try {
    await formRef.value?.validate()
    emit('success', {
      ...props.account,
      ...formData.value
    })
  } catch (error) {
    console.error('Form validation failed:', error)
  }
}
</script>

<style scoped>
.mfa-code-suffix {
  color: var(--app-text-secondary, #8b949e);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
  font-family: ui-monospace, SFMono-Regular, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New', monospace;
}
</style>
