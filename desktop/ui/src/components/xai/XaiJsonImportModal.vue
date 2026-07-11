<template>
  <n-modal
    v-model:show="visible"
    preset="card"
    :title="t('xai.jsonImportModalTitle')"
    style="width: 600px"
  >
    <n-input
      v-model:value="jsonText"
      type="textarea"
      :placeholder="t('xai.jsonImportPlaceholderText')"
      :rows="14"
    />
    <template #footer>
      <n-space justify="end">
        <n-button @click="visible = false">{{ t('common.cancel') }}</n-button>
        <n-button type="primary" :loading="importing" @click="handleImport">
          {{ t('xai.importButton') }}
        </n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { NModal, NInput, NButton, NSpace, useMessage, useDialog } from 'naive-ui'
import { useI18n } from 'vue-i18n'
import { useXaiAccountsStore } from '../../stores/xaiAccountsStore'
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
const importing = ref(false)

watch(() => props.show, (v) => {
  visible.value = v
  if (v) jsonText.value = ''
})
watch(visible, (v) => {
  if (!v) emit('update:show', false)
})

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

function parseAccounts(raw: unknown): XaiAccountInput[] {
  const list = Array.isArray(raw)
    ? raw
    : isRecord(raw) && Array.isArray(raw.accounts)
      ? raw.accounts
      : isRecord(raw)
        ? [raw]
        : []

  const out: XaiAccountInput[] = []
  for (const item of list) {
    if (!isRecord(item)) continue
    const refreshToken = String(item.refreshToken || item.refresh_token || '').trim()
    const accessToken = String(item.accessToken || item.access_token || '').trim()
    const apiKey = String(item.apiKey || item.api_key || '').trim()
    if (!refreshToken && !accessToken && !apiKey) continue
    out.push({
      email: String(item.email || '').trim(),
      subject: String(item.subject || item.sub || '').trim(),
      accessToken,
      refreshToken,
      idToken: String(item.idToken || item.id_token || '').trim(),
      apiKey,
      authKind: String(item.authKind || item.auth_kind || (apiKey && !refreshToken ? 'api_key' : 'oauth')),
      baseURL: String(item.baseURL || item.base_url || '').trim(),
      tokenEndpoint: String(item.tokenEndpoint || item.token_endpoint || '').trim(),
      redirectURI: String(item.redirectURI || item.redirect_uri || '').trim(),
      proxyUrl: String(item.proxyUrl || item.proxy_url || '').trim(),
      weight: Number(item.weight || 1) || 1,
      enabled: item.enabled !== false,
      websockets: Boolean(item.websockets),
      status: String(item.status || 'valid'),
      expiresAt: String(item.expiresAt || item.expired || '').trim()
    })
  }
  return out
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
  const accounts = parseAccounts(parsed)
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
