<template>
  <n-modal
    v-model:show="visible"
    preset="card"
    :title="t('xai.jsonImportModalTitle')"
    style="width: 600px"
  >
    <n-space vertical :size="16">
      <n-upload
        :custom-request="handleFileUpload"
        :show-file-list="false"
        accept=".json"
        multiple
      >
        <n-button>
          <template #icon>
            <n-icon><Upload /></n-icon>
          </template>
          {{ t('xai.selectJsonFiles') }}
        </n-button>
      </n-upload>

      <n-text v-if="fileCount > 0" depth="3">
        {{ t('xai.selectedFiles') }}: {{ fileCount }}
      </n-text>

      <n-divider>{{ t('xai.orPasteJson') }}</n-divider>

      <n-input
        v-model:value="jsonText"
        type="textarea"
        :placeholder="t('xai.jsonImportPlaceholderText')"
        :rows="12"
        :autosize="{ minRows: 12, maxRows: 20 }"
      />
    </n-space>

    <template #footer>
      <n-space justify="end">
        <n-button @click="handleCancel">{{ t('common.cancel') }}</n-button>
        <n-button type="primary" :loading="importing" @click="handleImport">
          {{ t('xai.importButton') }}
        </n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import type { UploadCustomRequestOptions } from 'naive-ui'
import { NModal, NSpace, NUpload, NButton, NIcon, NText, NDivider, NInput, useMessage, useDialog } from 'naive-ui'
import { Upload } from 'lucide-vue-next'
import { useI18n } from 'vue-i18n'
import { useXaiAccountsStore } from '../../stores/xaiAccountsStore'
import { parseXaiImportAccounts } from '@/utils/xaiAccountCopy'
import type { XaiAccountInput } from '@/types/xai'

const { t } = useI18n()
const message = useMessage()
const dialog = useDialog()
const xaiStore = useXaiAccountsStore()

const props = withDefaults(defineProps<{ show: boolean }>(), { show: false })
const emit = defineEmits<{
  'update:show': [show: boolean]
  success: []
}>()

const visible = ref(false)
const jsonText = ref('')
const fileCount = ref(0)
const importing = ref(false)

function toErrorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return String(error)
}

watch(() => props.show, (v) => {
  visible.value = v
  if (v) {
    jsonText.value = ''
    fileCount.value = 0
  }
})
watch(visible, (v) => {
  if (!v) emit('update:show', false)
})

function handleCancel(): void {
  visible.value = false
}

async function handleFileUpload(options: UploadCustomRequestOptions): Promise<void> {
  try {
    const rawFile = options.file.file
    if (!(rawFile instanceof File)) {
      throw new Error('Invalid file')
    }

    const payload: unknown = JSON.parse(await rawFile.text())
    const incoming = parseXaiImportAccounts(payload)
    if (incoming.length === 0) {
      throw new Error(t('xai.noValidAccounts'))
    }

    let accounts: XaiAccountInput[] = []
    if (jsonText.value.trim()) {
      accounts = parseXaiImportAccounts(JSON.parse(jsonText.value))
    }
    accounts.push(...incoming)
    jsonText.value = JSON.stringify(accounts, null, 2)
    fileCount.value = accounts.length
  } catch (error) {
    message.error(t('xai.fileParseErrors') + ': ' + toErrorMessage(error))
  } finally {
    options.onFinish()
  }
}

async function handleImport() {
  if (!jsonText.value.trim()) {
    message.warning(t('xai.pasteJsonContent'))
    return
  }
  let parsed: unknown
  try {
    parsed = JSON.parse(jsonText.value)
  } catch {
    message.error(t('xai.importFailed'))
    return
  }
  const accounts = parseXaiImportAccounts(parsed)
  if (accounts.length === 0) {
    message.warning(t('xai.noValidAccounts'))
    return
  }

  dialog.warning({
    title: t('common.confirm'),
    content: t('xai.importConfirm', { count: accounts.length }),
    positiveText: t('common.ok'),
    negativeText: t('common.cancel'),
    onPositiveClick: async () => {
      importing.value = true
      let success = 0
      let failed = 0
      try {
        for (const account of accounts) {
          try {
            await xaiStore.addAccount(account)
            success += 1
          } catch {
            failed += 1
          }
        }
        message.success(t('xai.importSuccess', { success, failed }))
        emit('success')
        visible.value = false
      } finally {
        importing.value = false
      }
    }
  })
}
</script>
