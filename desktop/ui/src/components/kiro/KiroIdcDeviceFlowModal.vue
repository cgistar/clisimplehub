<template>
  <n-modal
    :show="show"
    preset="card"
    :title="t('kiro.idcDialogTitle')"
    style="width: 620px"
    @update:show="handleShowUpdate"
  >
    <n-space vertical :size="16">
      <n-form-item :label="t('kiro.idcVerifyUrlLabel')">
        <n-input :value="verifyUrl || '—'" readonly />
      </n-form-item>

      <n-space>
        <n-button size="small" :disabled="!verifyUrl" @click="emit('copy-link')">{{ t('kiro.idcCopyLink') }}</n-button>
        <n-button size="small" :disabled="!verifyUrl" @click="emit('open-link')">{{ t('kiro.idcOpenLink') }}</n-button>
      </n-space>

      <n-alert :type="alertType" :title="statusLabel">
        {{ statusText }}
      </n-alert>

      <n-text depth="3">{{ t('kiro.idcDialogHelp') }}</n-text>
    </n-space>

    <template #footer>
      <n-space justify="end">
        <n-button :loading="loading" @click="emit('close')">{{ t('kiro.idcDialogClose') }}</n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { NModal, NSpace, NFormItem, NInput, NButton, NAlert, NText } from 'naive-ui'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

const props = withDefaults(
  defineProps<{
    show: boolean
    verifyUrl: string
    statusKind: 'idle' | 'polling' | 'pending' | 'ok' | 'error'
    statusText: string
    statusLabel: string
    loading?: boolean
  }>(),
  {
    show: false,
    verifyUrl: '',
    statusKind: 'idle',
    statusText: '',
    statusLabel: 'IDLE',
    loading: false
  }
)

const emit = defineEmits<{
  'update:show': [show: boolean]
  close: []
  'copy-link': []
  'open-link': []
}>()

const alertType = computed<'info' | 'success' | 'warning' | 'error'>(() => {
  if (props.statusKind === 'ok') return 'success'
  if (props.statusKind === 'error') return 'error'
  if (props.statusKind === 'pending' || props.statusKind === 'polling') return 'warning'
  return 'info'
})

function handleShowUpdate(nextShow: boolean): void {
  emit('update:show', nextShow)
  if (!nextShow) emit('close')
}
</script>
