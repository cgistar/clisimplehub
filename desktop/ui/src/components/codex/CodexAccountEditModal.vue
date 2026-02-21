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
        />
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
    </n-form>

    <template #footer>
      <n-space justify="end">
        <n-button @click="handleCancel">{{ t('common.cancel') }}</n-button>
        <n-button type="primary" @click="handleSave">{{ t('common.save') }}</n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import type { FormInst, FormRules } from 'naive-ui'
import { NModal, NForm, NFormItem, NInput, NInputNumber, NButton, NSpace } from 'naive-ui'
import { useI18n } from 'vue-i18n'
import type { CodexAccount, CodexAccountInput } from '@/types/codex'

const { t } = useI18n()

type EditFormData = CodexAccountInput & {
  accountId: string
  refreshToken: string
  password: string
  mfaCode: string
  proxyUrl: string
  weight: number
}

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
}>()

const visible = ref(false)
const formRef = ref<FormInst | null>(null)
const formData = ref<EditFormData>({
  accountId: '',
  refreshToken: '',
  password: '',
  mfaCode: '',
  proxyUrl: '',
  weight: 0
})

const rules: FormRules = {}

watch(() => props.show, (newVal) => {
  visible.value = newVal
  if (newVal && props.account) {
    formData.value = {
      accountId: props.account.accountId || '',
      refreshToken: props.account.refreshToken || '',
      password: props.account.password || '',
      mfaCode: props.account.mfaCode || '',
      proxyUrl: props.account.proxyUrl || '',
      weight: props.account.weight || 0
    }
  }
})

watch(visible, (newVal) => {
  if (!newVal) {
    emit('update:show', false)
  }
})

function handleCancel() {
  visible.value = false
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
