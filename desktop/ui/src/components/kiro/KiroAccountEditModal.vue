<template>
  <n-modal
    v-model:show="visible"
    preset="card"
    :title="t('kiro.editAccountTitle')"
    style="width: 680px"
  >
    <n-form label-placement="top" :model="form">
      <n-grid :cols="24" :x-gap="12">
        <n-form-item-gi :span="24" :label="t('kiro.refreshToken')">
          <n-input v-model:value="form.refreshToken" disabled />
        </n-form-item-gi>

        <n-form-item-gi :span="12" :label="t('kiro.region')">
          <n-input v-model:value="form.region" placeholder="us-east-1" />
        </n-form-item-gi>

        <n-form-item-gi :span="12" :label="t('kiro.authMethod')">
          <n-select v-model:value="form.authMethod" :options="authMethodOptions" />
        </n-form-item-gi>

        <n-form-item-gi :span="12" :label="t('kiro.provider')">
          <n-input v-model:value="form.provider" :placeholder="t('kiro.providerPlaceholder')" />
        </n-form-item-gi>

        <n-form-item-gi v-if="showProfileArn" :span="12" :label="t('kiro.profileArn')">
          <n-input v-model:value="form.profileArn" :placeholder="t('kiro.profileArnPlaceholder')" />
        </n-form-item-gi>

        <n-form-item-gi v-if="showIdcFields" :span="12" :label="t('kiro.clientId')">
          <n-input v-model:value="form.clientId" :placeholder="t('kiro.clientIdPlaceholder')" />
        </n-form-item-gi>

        <n-form-item-gi v-if="showIdcFields" :span="12" :label="t('kiro.clientSecret')">
          <n-input
            v-model:value="form.clientSecret"
            type="password"
            show-password-on="click"
            :placeholder="t('kiro.clientSecretPlaceholder')"
          />
        </n-form-item-gi>

        <n-form-item-gi :span="24">
          <n-text depth="3">{{ t('kiro.perAccountConfig') }}</n-text>
        </n-form-item-gi>

        <n-form-item-gi :span="12" :label="t('kiro.proxyUrl')">
          <n-input v-model:value="form.proxyUrl" :placeholder="t('kiro.proxyUrlPlaceholder')" />
        </n-form-item-gi>

        <n-form-item-gi :span="12" :label="t('kiro.userAgent')">
          <n-input v-model:value="form.userAgent" :placeholder="t('kiro.userAgentPlaceholder')" />
        </n-form-item-gi>

        <n-form-item-gi :span="8" :label="t('kiro.version')">
          <n-input v-model:value="form.version" :placeholder="t('kiro.versionPlaceholder')" />
        </n-form-item-gi>

        <n-form-item-gi :span="8" :label="t('kiro.weight')">
          <n-input-number v-model:value="form.weight" :min="1" :max="9999" style="width: 100%" />
        </n-form-item-gi>

        <n-form-item-gi :span="8" label="MachineId">
          <n-input v-model:value="form.machineId" placeholder="Optional unique machine identifier" />
        </n-form-item-gi>
      </n-grid>
    </n-form>

    <template #footer>
      <n-space justify="end">
        <n-button @click="close">{{ t('common.cancel') }}</n-button>
        <n-button type="primary" :loading="saving" @click="submit">{{ t('common.save') }}</n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import {
  NModal,
  NForm,
  NFormItemGi,
  NInput,
  NInputNumber,
  NButton,
  NSpace,
  NGrid,
  NSelect,
  NText,
  useMessage
} from 'naive-ui'
import { useI18n } from 'vue-i18n'
import type { KiroAccount, KiroAccountInput } from '@/types/kiro'

const { t } = useI18n()
const message = useMessage()

const props = withDefaults(
  defineProps<{
    show: boolean
    account: KiroAccount | null
  }>(),
  {
    show: false,
    account: null
  }
)

const emit = defineEmits<{
  'update:show': [show: boolean]
  success: [payload: KiroAccountInput]
}>()

const visible = ref(false)
const saving = ref(false)

const authMethodOptions = computed(() => [
  { label: 'social', value: 'social' },
  { label: 'idc', value: 'idc' }
])

const form = reactive<KiroAccountInput>({
  refreshToken: '',
  region: 'us-east-1',
  authMethod: 'social',
  provider: '',
  profileArn: '',
  clientId: '',
  clientSecret: '',
  proxyUrl: '',
  userAgent: '',
  version: '',
  weight: 1,
  machineId: ''
})

const showIdcFields = computed(() => String(form.authMethod || '').toLowerCase() === 'idc')
const showProfileArn = computed(() => !showIdcFields.value)

function fillForm(account: KiroAccount | null): void {
  if (!account) return

  form.refreshToken = account.refreshToken || ''
  form.region = account.region || 'us-east-1'
  form.authMethod = account.authMethod || 'social'
  form.provider = account.provider || ''
  form.profileArn = account.profileArn || ''
  form.clientId = account.clientId || ''
  form.clientSecret = account.clientSecret || ''
  form.proxyUrl = account.proxyUrl || ''
  form.userAgent = account.userAgent || ''
  form.version = account.version || ''
  form.weight = account.weight && account.weight > 0 ? account.weight : 1
  form.machineId = account.machineId || ''
}

watch(
  () => props.show,
  (show) => {
    visible.value = show
    if (show) fillForm(props.account)
  },
  { immediate: true }
)

watch(visible, (show) => {
  if (!show) emit('update:show', false)
})

watch(
  () => props.account,
  (account) => {
    if (!visible.value) return
    fillForm(account)
  }
)

function close(): void {
  visible.value = false
}

async function submit(): Promise<void> {
  if (!form.refreshToken) {
    message.error(t('kiro.refreshTokenRequired'))
    return
  }

  if (showIdcFields.value && (!String(form.clientId || '').trim() || !String(form.clientSecret || '').trim())) {
    message.error(t('kiro.idcFieldsRequired'))
    return
  }

  saving.value = true
  try {
    emit('success', {
      ...form,
      region: String(form.region || 'us-east-1').trim(),
      authMethod: String(form.authMethod || 'social').trim().toLowerCase()
    })
    close()
  } finally {
    saving.value = false
  }
}
</script>
