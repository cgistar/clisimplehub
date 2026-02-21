<template>
  <n-modal
    :show="show"
    preset="card"
    :title="t('kiro.kiroSignLoginTitle')"
    style="width: 600px"
    :mask-closable="false"
    @update:show="handleShowUpdate"
  >
    <n-form>
      <n-form-item :label="t('kiro.loginUrlLabel')">
        <n-input-group>
          <n-input
            :value="loginUrl || '—'"
            readonly
            @click="handleSelectUrl"
          />
          <n-button :disabled="!loginUrl" @click="emit('copy-link')">
            <template #icon><n-icon><Copy /></n-icon></template>
          </n-button>
          <n-button :disabled="!loginUrl" @click="emit('open-link')">
            <template #icon><n-icon><ExternalLink /></n-icon></template>
          </n-button>
          <n-button :disabled="!loginUrl" @click="emit('open-incognito')">
            <template #icon><n-icon><EyeOff /></n-icon></template>
          </n-button>
        </n-input-group>
      </n-form-item>
    </n-form>

    <n-alert type="info" :bordered="false">
      <template #icon>
        <n-spin size="small" />
      </template>
      {{ t('kiro.kiroSignLoginWaiting') }}
    </n-alert>

    <template #footer>
      <n-space justify="end">
        <n-button @click="handleCancel">{{ t('common.cancel') }}</n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
import { NModal, NForm, NFormItem, NInput, NInputGroup, NButton, NAlert, NSpin, NIcon, NSpace } from 'naive-ui'
import { Copy, ExternalLink, EyeOff } from 'lucide-vue-next'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

const props = withDefaults(
  defineProps<{
    show: boolean
    waiting: boolean
    loginUrl: string
  }>(),
  {
    show: false,
    waiting: false,
    loginUrl: ''
  }
)

const emit = defineEmits<{
  'update:show': [show: boolean]
  close: []
  'copy-link': []
  'open-link': []
  'open-incognito': []
}>()

function handleShowUpdate(nextShow: boolean): void {
  emit('update:show', nextShow)
  if (!nextShow) emit('close')
}

function handleCancel(): void {
  emit('update:show', false)
  emit('close')
}

function handleSelectUrl(event: Event): void {
  const target = event.target as HTMLInputElement | null
  target?.select()
}
</script>
