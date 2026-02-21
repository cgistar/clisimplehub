<template>
  <n-modal
    v-model:show="visible"
    preset="card"
    :title="t('kiro.jsonImportTitle')"
    style="width: 640px"
  >
    <n-space vertical :size="16">
      <n-upload :custom-request="handleFileUpload" :show-file-list="false" accept=".json" multiple>
        <n-button>
          <template #icon>
            <n-icon><Upload /></n-icon>
          </template>
          {{ t('kiro.selectJsonFiles') }}
        </n-button>
      </n-upload>

      <n-text v-if="fileCount > 0" depth="3">
        {{ t('kiro.selectedFiles') }}: {{ fileCount }}
      </n-text>

      <n-divider>{{ t('kiro.orPasteJson') }}</n-divider>

      <n-input
        v-model:value="jsonText"
        type="textarea"
        :placeholder="t('kiro.jsonImportPlaceholder')"
        :rows="12"
        :autosize="{ minRows: 12, maxRows: 20 }"
      />
    </n-space>

    <template #footer>
      <n-space justify="end">
        <n-button @click="close">{{ t('common.cancel') }}</n-button>
        <n-button type="primary" :loading="importing" @click="handleImport">{{ t('kiro.import') }}</n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import type { UploadCustomRequestOptions } from 'naive-ui'
import { NModal, NSpace, NUpload, NButton, NIcon, NText, NDivider, NInput, useDialog, useMessage } from 'naive-ui'
import { Upload } from 'lucide-vue-next'
import { useI18n } from 'vue-i18n'
import type { KiroAccountInput } from '@/types/kiro'

interface BuildImportResult {
  dtos: KiroAccountInput[]
  errors: string[]
}

const { t } = useI18n()
const message = useMessage()
const dialog = useDialog()

const props = withDefaults(
  defineProps<{
    show: boolean
  }>(),
  {
    show: false
  }
)

const emit = defineEmits<{
  'update:show': [show: boolean]
  success: [accounts: KiroAccountInput[]]
}>()

const visible = ref(false)
const jsonText = ref('')
const fileCount = ref(0)
const importing = ref(false)

function toErrorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return String(error)
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

watch(
  () => props.show,
  (show) => {
    visible.value = show
    if (show) {
      jsonText.value = ''
      fileCount.value = 0
    }
  },
  { immediate: true }
)

watch(visible, (show) => {
  if (!show) emit('update:show', false)
})

function close(): void {
  visible.value = false
}

async function handleFileUpload(options: UploadCustomRequestOptions): Promise<void> {
  try {
    const rawFile = options.file.file
    if (!(rawFile instanceof File)) {
      throw new Error('Invalid file')
    }

    const text = await rawFile.text()
    const data = JSON.parse(text)

    const existing = jsonText.value.trim() ? buildImportDTOs(jsonText.value).dtos : []
    const incoming = buildImportDTOs(JSON.stringify(data)).dtos

    const merged = [...existing, ...incoming]
    jsonText.value = JSON.stringify(merged, null, 2)
    fileCount.value = merged.length
  } catch (error) {
    message.error(t('kiro.jsonImportPickFileFailed') + toErrorMessage(error))
  } finally {
    options.onFinish()
  }
}

function normalizeAuthMethod(value: string): '' | 'social' | 'idc' {
  const normalized = String(value || '').trim().toLowerCase()
  if (normalized === 'social' || normalized === 'idc') return normalized
  return ''
}

function normalizeSocialProvider(provider: string): string {
  const raw = String(provider || '').trim()
  if (!raw) return ''

  const lower = raw.toLowerCase()
  if (lower === 'google') return 'Google'
  if (lower === 'github' || lower === 'git-hub' || lower === 'git_hub') return 'Github'
  return raw
}

function looksLikeBuilderProvider(provider: string): boolean {
  const lower = String(provider || '').trim().toLowerCase()
  return lower === 'builderid' || lower === 'builder-id' || lower === 'builder_id' || lower === 'builder'
}

function getStringField(item: Record<string, unknown>, keys: string[]): string {
  for (const key of keys) {
    const value = item[key]
    if (typeof value === 'string') {
      const trimmed = value.trim()
      if (trimmed) return trimmed
    }
  }
  return ''
}

function parseImportItems(payload: unknown): Array<Record<string, unknown>> {
  if (Array.isArray(payload)) return payload.filter(isRecord)

  if (isRecord(payload)) {
    if (Array.isArray(payload.accounts)) return payload.accounts.filter(isRecord)
    if (Array.isArray(payload.Accounts)) return payload.Accounts.filter(isRecord)
    return [payload]
  }

  return []
}

function buildImportDTOs(rawText: string): BuildImportResult {
  const errors: string[] = []
  let payload: unknown

  try {
    payload = JSON.parse(rawText)
  } catch (error) {
    return {
      dtos: [],
      errors: [t('kiro.jsonImportParseFailed') + toErrorMessage(error)]
    }
  }

  const items = parseImportItems(payload)
  const dtos: KiroAccountInput[] = []
  const seen = new Set<string>()

  items.forEach((item, index) => {
    const refreshToken = getStringField(item, ['refreshToken', 'refresh_token', 'RefreshToken'])
    if (!refreshToken) {
      errors.push(`#${index + 1}: missing refreshToken`)
      return
    }
    if (!refreshToken.startsWith('aor')) {
      errors.push(`#${index + 1}: refreshToken should start with \"aor\"`)
      return
    }
    if (seen.has(refreshToken)) {
      errors.push(`#${index + 1}: duplicated refreshToken`)
      return
    }
    seen.add(refreshToken)

    const clientId = getStringField(item, ['clientId', 'client_id', 'ClientId'])
    const clientSecret = getStringField(item, ['clientSecret', 'client_secret', 'ClientSecret'])
    const explicitAuthMethod = normalizeAuthMethod(getStringField(item, ['authMethod', 'auth_method', 'AuthMethod']))

    let provider = getStringField(item, ['provider', 'Provider'])
    const inferredAuthMethod = clientId && clientSecret ? 'idc' : 'social'
    const authMethod =
      explicitAuthMethod === 'idc'
        ? 'idc'
        : explicitAuthMethod === 'social' && (Boolean(clientId && clientSecret) || looksLikeBuilderProvider(provider))
          ? 'idc'
          : explicitAuthMethod || inferredAuthMethod

    if (authMethod === 'idc') {
      if (!clientId || !clientSecret) {
        errors.push(`#${index + 1}: IdC account requires both clientId and clientSecret`)
        return
      }
    }

    if (authMethod === 'social') {
      provider = normalizeSocialProvider(provider) || 'Google'
      if (!['Google', 'Github'].includes(provider)) {
        errors.push(`#${index + 1}: invalid provider for social account (Google/Github)`)
        return
      }
    }

    const dto: KiroAccountInput = {
      refreshToken,
      authMethod,
      provider,
      clientId,
      clientSecret,
      region: getStringField(item, ['region', 'Region']) || 'us-east-1',
      accessToken: getStringField(item, ['accessToken', 'access_token', 'AccessToken']),
      profileArn: getStringField(item, ['profileArn', 'profile_arn', 'ProfileArn']),
      expiresAt: getStringField(item, ['expiresAt', 'expires_at', 'ExpiresAt']),
      email: getStringField(item, ['email', 'Email']),
      machineId: getStringField(item, ['machineId', 'machine_id', 'MachineId'])
    }

    Object.keys(dto).forEach((key) => {
      const typedKey = key as keyof KiroAccountInput
      if (!dto[typedKey]) delete dto[typedKey]
    })

    dtos.push(dto)
  })

  return { dtos, errors }
}

async function confirmImport(content: string, mode: 'warning' | 'info'): Promise<boolean> {
  return new Promise((resolve) => {
    const handler = mode === 'warning' ? dialog.warning : dialog.info
    handler({
      title: t('common.confirm'),
      content,
      positiveText: t('common.confirm'),
      negativeText: t('common.cancel'),
      onPositiveClick: () => resolve(true),
      onNegativeClick: () => resolve(false)
    })
  })
}

async function handleImport(): Promise<void> {
  const raw = jsonText.value.trim()
  if (!raw) {
    message.error(t('kiro.jsonImportNoValidAccounts'))
    return
  }

  importing.value = true
  try {
    const { dtos, errors } = buildImportDTOs(raw)

    if (!dtos.length) {
      if (errors.length) {
        const max = 3
        const details = errors.slice(0, max).join('\n') + (errors.length > max ? `\n...(${errors.length})` : '')
        message.error(details)
      } else {
        message.error(t('kiro.jsonImportNoValidAccounts'))
      }
      return
    }

    if (errors.length) {
      const confirmed = await confirmImport(
        `${t('kiro.jsonImportConfirmPrefix')}${dtos.length}${t('kiro.jsonImportConfirmSuffix')}\n` +
          `${t('kiro.jsonImportInvalidSkippedPrefix')}${errors.length}${t('kiro.jsonImportInvalidSkippedSuffix')}`,
        'warning'
      )
      if (!confirmed) return
    } else {
      const confirmed = await confirmImport(
        `${t('kiro.jsonImportConfirmPrefix')}${dtos.length}${t('kiro.jsonImportConfirmSuffix')}`,
        'info'
      )
      if (!confirmed) return
    }

    emit('success', dtos)
    close()
  } catch (error) {
    message.error(t('kiro.jsonImportFailedPrefix') + toErrorMessage(error))
  } finally {
    importing.value = false
  }
}
</script>
