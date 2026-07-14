<template>
  <n-modal
    v-model:show="visible"
    preset="card"
    :title="t('xai.ssoImportTitle')"
    style="width: 680px"
    :mask-closable="!importing"
    :close-on-esc="!importing"
    :closable="!importing"
  >
    <n-space vertical :size="16">
      <n-text depth="3">{{ t('xai.ssoImportDescription') }}</n-text>
      <n-input
        v-model:value="text"
        type="textarea"
        :rows="12"
        :autosize="{ minRows: 12, maxRows: 20 }"
        :placeholder="t('xai.ssoImportPlaceholder')"
        :disabled="importing"
      />

      <n-alert v-if="importing" type="info" :bordered="false">
        {{ t('xai.ssoImportProgress', { current: progress, total }) }}
      </n-alert>

      <n-space v-if="result" wrap>
        <n-tag type="success">{{ t('xai.ssoImportCreated') }}: {{ result.created }}</n-tag>
        <n-tag type="info">{{ t('xai.ssoImportUpdated') }}: {{ result.updated }}</n-tag>
        <n-tag type="error">{{ t('xai.ssoImportFailed') }}: {{ result.failures.length }}</n-tag>
        <n-tag type="warning">{{ t('xai.ssoImportWarnings') }}: {{ result.warnings.length }}</n-tag>
        <n-tag>{{ t('xai.ssoImportSkipped') }}: {{ result.skipped }}</n-tag>
      </n-space>

      <n-alert v-if="result?.failures.length" type="error" :title="t('xai.ssoImportFailed')">
        <div v-for="item in result.failures" :key="`failure-${item.line}`">
          {{ t('xai.ssoImportLineError', { line: item.line, error: item.message }) }}
        </div>
      </n-alert>
      <n-alert v-if="result?.warnings.length" type="warning" :title="t('xai.ssoImportWarnings')">
        <div v-for="item in result.warnings" :key="`warning-${item.line}`">
          {{ t('xai.ssoImportLineWarning', { line: item.line, warning: item.message }) }}
        </div>
      </n-alert>
    </n-space>

    <template #footer>
      <n-space justify="end">
        <n-button :disabled="importing" @click="visible = false">{{ t('common.close') }}</n-button>
        <n-button type="primary" :loading="importing" @click="handleImport">
          {{ t('xai.importButton') }}
        </n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { NAlert, NButton, NInput, NModal, NSpace, NTag, NText, useMessage } from 'naive-ui'
import { useI18n } from 'vue-i18n'
import { xaiApi } from '@/api/xai'

interface LineMessage {
  line: number
  message: string
}

interface ImportSummary {
  created: number
  updated: number
  skipped: number
  failures: LineMessage[]
  warnings: LineMessage[]
}

const props = withDefaults(defineProps<{ show: boolean }>(), { show: false })
const emit = defineEmits<{
  'update:show': [show: boolean]
  success: []
}>()
const { t } = useI18n()
const message = useMessage()
const visible = ref(false)
const text = ref('')
const importing = ref(false)
const progress = ref(0)
const total = ref(0)
const result = ref<ImportSummary | null>(null)

watch(() => props.show, (show) => {
  visible.value = show
  if (show) {
    text.value = ''
    progress.value = 0
    total.value = 0
    result.value = null
  }
})
watch(visible, (show) => {
  if (!show) emit('update:show', false)
})

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

async function handleImport(): Promise<void> {
  if (importing.value) return
  const seen = new Set<string>()
  const entries: Array<{ line: number; sso: string }> = []
  let skipped = 0
  for (const [index, raw] of text.value.split(/\r?\n/).entries()) {
    const sso = raw.trim()
    if (!sso) continue
    if (seen.has(sso)) {
      skipped += 1
      continue
    }
    seen.add(sso)
    entries.push({ line: index + 1, sso })
  }
  if (entries.length === 0) {
    message.warning(t('xai.ssoImportEmpty'))
    return
  }

  importing.value = true
  progress.value = 0
  total.value = entries.length
  const summary: ImportSummary = { created: 0, updated: 0, skipped, failures: [], warnings: [] }
  try {
    for (const entry of entries) {
      progress.value += 1
      try {
        const imported = await xaiApi.importSSOAccount(entry.sso)
        if (!imported.success) throw new Error(imported.error || 'SSO import failed')
        if (imported.action === 'created') summary.created += 1
        else summary.updated += 1
        if (imported.warning) summary.warnings.push({ line: entry.line, message: imported.warning })
      } catch (error) {
        summary.failures.push({ line: entry.line, message: errorMessage(error) })
      }
    }
    result.value = summary
    if (summary.created + summary.updated > 0) emit('success')
  } finally {
    importing.value = false
  }
}
</script>
