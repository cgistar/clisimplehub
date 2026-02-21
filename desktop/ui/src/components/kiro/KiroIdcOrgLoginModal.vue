<template>
  <n-modal
    :show="show"
    preset="card"
    :title="t('kiro.idcOrgDialogTitle')"
    style="width: 640px"
    @update:show="handleShowUpdate"
  >
    <n-space vertical :size="16">
      <template v-if="step === 'config'">
        <n-form-item :label="t('kiro.idcOrgStartUrl')">
          <n-input
            :value="startUrl"
            :placeholder="t('kiro.idcOrgStartUrlPlaceholder')"
            @update:value="(val) => emit('update:start-url', val)"
          />
          <n-text depth="3">{{ t('kiro.idcOrgStartUrlHelp') }}</n-text>
        </n-form-item>

        <n-form-item :label="t('kiro.region')">
          <n-input :value="region" placeholder="us-east-1" @update:value="(val) => emit('update:region', val)" />
          <n-text depth="3">{{ t('kiro.idcOrgRegionHelp') }}</n-text>
        </n-form-item>
      </template>

      <template v-else>
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
      </template>
    </n-space>

    <template #footer>
      <n-space justify="space-between" style="width: 100%">
        <n-button v-if="step === 'verify'" @click="emit('back')">{{ t('kiro.idcOrgBack') }}</n-button>
        <span v-else></span>

        <n-space>
          <n-button @click="emit('close')">{{ t('kiro.idcDialogClose') }}</n-button>
          <n-button
            v-if="step === 'config'"
            type="primary"
            :loading="loading"
            @click="emit('connect')"
          >
            {{ t('kiro.idcOrgConnect') }}
          </n-button>
        </n-space>
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
    step: 'config' | 'verify'
    startUrl: string
    region: string
    verifyUrl: string
    statusKind: 'idle' | 'polling' | 'pending' | 'ok' | 'error'
    statusText: string
    statusLabel: string
    loading?: boolean
  }>(),
  {
    show: false,
    step: 'config',
    startUrl: '',
    region: 'us-east-1',
    verifyUrl: '',
    statusKind: 'idle',
    statusText: '',
    statusLabel: 'IDLE',
    loading: false
  }
)

const emit = defineEmits<{
  'update:show': [show: boolean]
  'update:start-url': [startUrl: string]
  'update:region': [region: string]
  connect: []
  back: []
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
