<template>
  <n-modal
    v-model:show="visible"
    preset="card"
    :title="t('codex.jsonImportModalTitle')"
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
          {{ t('codex.selectJsonFiles') }}
        </n-button>
      </n-upload>

      <n-text v-if="fileCount > 0" depth="3">
        {{ t('codex.selectedFiles') }}: {{ fileCount }}
      </n-text>

      <n-divider>{{ t('codex.orPasteJson') }}</n-divider>

      <n-input
        v-model:value="jsonText"
        type="textarea"
        :placeholder="t('codex.jsonImportPlaceholderText')"
        :rows="12"
        :autosize="{ minRows: 12, maxRows: 20 }"
      />
    </n-space>

    <template #footer>
      <n-space justify="end">
        <n-button @click="handleCancel">{{ t('common.cancel') }}</n-button>
        <n-button type="primary" @click="handleImport" :loading="importing">
          {{ t('codex.importButton') }}
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
import { useCodexAccountsStore } from '../../stores/codexAccountsStore'
import type { CodexAccountInput } from '@/types/codex'

const { t } = useI18n()
const message = useMessage()
const dialog = useDialog()
const codexStore = useCodexAccountsStore()

const props = withDefaults(defineProps<{
  show: boolean
}>(), {
  show: false
})

const emit = defineEmits<{
  'update:show': [show: boolean]
  success: []
}>()

const visible = ref(false)
const jsonText = ref('')
const fileCount = ref(0)
const importing = ref(false)

interface BuildImportResult {
  dtos: CodexAccountInput[]
  errors: string[]
}

interface CodexJwtFields {
  accountId: string
  email: string
  planType: string
  expiresAt: string
}

function toErrorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return String(error)
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

watch(() => props.show, (newVal) => {
  visible.value = newVal
  if (newVal) {
    jsonText.value = ''
    fileCount.value = 0
  }
})

watch(visible, (newVal) => {
  if (!newVal) {
    emit('update:show', false)
  }
})

async function handleFileUpload(options: UploadCustomRequestOptions): Promise<void> {
  try {
    const rawFile = options.file.file
    if (!(rawFile instanceof File)) {
      throw new Error('Invalid file')
    }

    const text = await rawFile.text()
    const data: unknown = JSON.parse(text)
    const account = parseCodexJsonFile(data)

    if (account) {
      const accounts = jsonText.value ? buildCodexImportDTOs(jsonText.value).dtos : []
      accounts.push(account)
      jsonText.value = JSON.stringify(accounts, null, 2)
      fileCount.value = accounts.length
    } else {
      message.error(t('codex.fileParseErrors'))
    }
  } catch (error) {
    message.error(t('codex.fileParseErrors') + ': ' + toErrorMessage(error))
  } finally {
    options.onFinish()
  }
}

function parseCodexJsonFile(data: unknown): CodexAccountInput | null {
  if (!isRecord(data)) return null

  const accessToken = getStringField(data, ['access_token', 'accessToken'])
  const refreshToken = getStringField(data, ['refresh_token', 'refreshToken'])
  const jwtFields = getJwtFields(accessToken)
  const account: CodexAccountInput = {
    accessToken,
    idToken: getStringField(data, ['id_token', 'idToken']),
    accountId: getStringField(data, ['account_id', 'accountId']) || jwtFields.accountId,
    email: getStringField(data, ['email']) || jwtFields.email,
    password: getStringField(data, ['password', 'Password']),
    planType: getPlanType(data) || jwtFields.planType || 'free',
    expiresAt: getStringField(data, ['expired', 'expiresAt', 'expires_at', 'ExpiresAt']) || jwtFields.expiresAt
  }

  if (refreshToken) {
    account.refreshToken = refreshToken
  }

  if (!account.accessToken || !account.accountId || !account.email) {
    return null
  }
  if (!account.refreshToken && !account.expiresAt) {
    return null
  }

  return account
}

function getPlanType(data: Record<string, unknown>): string {
  const authData = data['https://api.openai.com/auth']
  if (isRecord(authData)) {
    const fromAuth = getStringField(authData, ['chatgpt_plan_type'])
    if (fromAuth) return fromAuth
  }
  return getStringField(data, ['planType'])
}

function getStringFromRecord(item: Record<string, unknown> | undefined, keys: string[]): string {
  if (!item) return ''
  return getStringField(item, keys)
}

function decodeBase64Url(value: string): string {
  const base64 = value.replace(/-/g, '+').replace(/_/g, '/')
  const padded = base64.padEnd(base64.length + ((4 - (base64.length % 4)) % 4), '=')
  const binary = atob(padded)
  const bytes = Uint8Array.from(binary, (char) => char.charCodeAt(0))
  return new TextDecoder().decode(bytes)
}

function readJwtClaims(token: string): Record<string, unknown> | null {
  const parts = token.split('.')
  if (parts.length !== 3 || !parts[1]) return null

  try {
    const claims: unknown = JSON.parse(decodeBase64Url(parts[1]))
    return isRecord(claims) ? claims : null
  } catch {
    return null
  }
}

function getJwtExpiresAt(claims: Record<string, unknown> | null): string {
  const rawExp = claims?.exp
  const exp = typeof rawExp === 'number' ? rawExp : typeof rawExp === 'string' ? Number(rawExp) : 0
  if (!Number.isFinite(exp) || exp <= 0) return ''
  return new Date(exp * 1000).toISOString()
}

function getJwtFields(accessToken: string): CodexJwtFields {
  const claims = readJwtClaims(accessToken)
  const authData = isRecord(claims?.['https://api.openai.com/auth'])
    ? claims['https://api.openai.com/auth']
    : undefined
  const profileData = isRecord(claims?.['https://api.openai.com/profile'])
    ? claims['https://api.openai.com/profile']
    : undefined

  return {
    accountId: getStringFromRecord(authData, ['chatgpt_account_id']),
    email: getStringFromRecord(claims || undefined, ['email']) || getStringFromRecord(profileData, ['email']),
    planType: getStringFromRecord(authData, ['chatgpt_plan_type']),
    expiresAt: getJwtExpiresAt(claims)
  }
}

function handleCancel(): void {
  visible.value = false
}

async function handleImport(): Promise<void> {
  if (!jsonText.value.trim()) {
    message.error(t('codex.pasteJsonContent'))
    return
  }

  try {
    importing.value = true
    const { dtos, errors } = buildCodexImportDTOs(jsonText.value)

    if (!dtos.length) {
      message.error(errors.length ? errors.slice(0, 3).join('\n') : t('codex.noValidAccounts'))
      return
    }

    if (errors.length) {
      const confirmed = await confirmImport(
        t('codex.importConfirmWithErrors', {
          count: dtos.length,
          errors: errors.length
        }),
        'warning'
      )
      if (!confirmed) return
    } else {
      const confirmed = await confirmImport(
        t('codex.importConfirm', { count: dtos.length }),
        'info'
      )
      if (!confirmed) return
    }

    let successCount = 0
    let failedCount = 0
    let skippedCount = 0

    for (const dto of dtos) {
      try {
        await codexStore.addAccount(dto)
        successCount += 1
      } catch (error) {
        const reason = toErrorMessage(error)
        if (reason.includes('already exists') || reason.includes('duplicate')) {
          skippedCount += 1
        } else {
          failedCount += 1
        }
      }
    }

    let resultMessage = t('codex.importSuccess', {
      success: successCount,
      failed: failedCount
    })

    if (skippedCount > 0) {
      resultMessage += ` (${t('codex.skippedDuplicate')}: ${skippedCount})`
    }

    message.success(resultMessage)
    visible.value = false
    emit('success')
  } catch (error) {
    message.error(t('codex.importFailed') + ': ' + toErrorMessage(error))
  } finally {
    importing.value = false
  }
}

async function confirmImport(content: string, mode: 'warning' | 'info'): Promise<boolean> {
  return new Promise<boolean>((resolve) => {
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

function buildCodexImportDTOs(rawJsonText: string): BuildImportResult {
  const errors: string[] = []
  let payload: unknown

  try {
    payload = JSON.parse(rawJsonText)
  } catch (error) {
    return { dtos: [], errors: ['JSON parse failed: ' + toErrorMessage(error)] }
  }

  const items = parseImportItems(payload)
  const dtos: CodexAccountInput[] = []
  const seen = new Set<string>()

  items.forEach((item, index) => {
    const refreshToken = getStringField(item, ['refreshToken', 'refresh_token', 'RefreshToken'])
    const accessToken = getStringField(item, ['accessToken', 'access_token', 'AccessToken'])
    const jwtFields = getJwtFields(accessToken)
    const accountId = getStringField(item, ['accountId', 'account_id', 'AccountId']) || jwtFields.accountId
    if (!accountId) {
      errors.push(`#${index + 1}: missing accountId`)
      return
    }
    const email = getStringField(item, ['email', 'Email']) || jwtFields.email
    if (!email) {
      errors.push(`#${index + 1}: missing email`)
      return
    }
    const localKey = `${accountId.trim().toLowerCase()}\x00${email.trim().toLowerCase()}`
    if (seen.has(localKey)) {
      errors.push(`#${index + 1}: duplicate account id`)
      return
    }
    seen.add(localKey)

    if (!refreshToken && !accessToken) {
      errors.push(`#${index + 1}: missing refreshToken/accessToken`)
      return
    }
    const expiresAt = getStringField(item, ['expiresAt', 'expires_at', 'expired', 'ExpiresAt']) || jwtFields.expiresAt
    if (!refreshToken && !expiresAt) {
      errors.push(`#${index + 1}: missing expiresAt/accessToken exp`)
      return
    }

    const dto: CodexAccountInput = {
      accountId,
      refreshToken,
      accessToken,
      idToken: getStringField(item, ['idToken', 'id_token', 'IdToken']),
      expiresAt,
      email,
      password: getStringField(item, ['password', 'Password']),
      planType: getStringField(item, ['planType', 'plan_type', 'PlanType']) || jwtFields.planType,
      proxyUrl: getStringField(item, ['proxyUrl', 'proxy_url', 'ProxyUrl'])
    }

    const weightStr = getStringField(item, ['weight', 'Weight'])
    if (weightStr) dto.weight = parseInt(weightStr, 10) || 0

    removeEmptyFields(dto)
    dtos.push(dto)
  })

  return { dtos, errors }
}

function parseImportItems(payload: unknown): Array<Record<string, unknown>> {
  if (Array.isArray(payload)) {
    return payload.filter(isRecord)
  }

  if (isRecord(payload)) {
    const accounts = payload.accounts
    if (Array.isArray(accounts)) return accounts.filter(isRecord)

    const accountsUpper = payload.Accounts
    if (Array.isArray(accountsUpper)) return accountsUpper.filter(isRecord)

    return [payload]
  }

  return []
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

function removeEmptyFields(dto: CodexAccountInput): void {
  const fields = Object.entries(dto) as Array<[keyof CodexAccountInput, CodexAccountInput[keyof CodexAccountInput]]>
  for (const [key, value] of fields) {
    if (value === '' || value === undefined || value === null) {
      delete dto[key]
    }
  }
}
</script>
