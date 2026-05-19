import type { EndpointImportInput, EndpointModelMapping } from '@/types'

interface BuildEndpointImportResult {
  dtos: EndpointImportInput[]
  errors: string[]
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
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

function getNumberField(item: Record<string, unknown>, keys: string[]): number | undefined {
  for (const key of keys) {
    const value = item[key]
    if (typeof value === 'number' && Number.isFinite(value)) return value
    if (typeof value === 'string' && value.trim()) {
      const parsed = Number(value)
      if (Number.isFinite(parsed)) return parsed
    }
  }
  return undefined
}

function getBooleanField(item: Record<string, unknown>, keys: string[]): boolean | undefined {
  for (const key of keys) {
    const value = item[key]
    if (typeof value === 'boolean') return value
    if (typeof value === 'string') {
      const normalized = value.trim().toLowerCase()
      if (normalized === 'true') return true
      if (normalized === 'false') return false
    }
  }
  return undefined
}

function getStringArrayField(item: Record<string, unknown>, keys: string[]): string[] | undefined {
  for (const key of keys) {
    const value = item[key]
    if (!Array.isArray(value)) continue
    const values = value.filter((entry): entry is string => typeof entry === 'string').map((entry) => entry.trim()).filter(Boolean)
    return values.length > 0 ? values : undefined
  }
  return undefined
}

function getHeadersField(item: Record<string, unknown>): Record<string, string> | undefined {
  const value = item.headers || item.Headers
  if (!isRecord(value)) return undefined
  const headers: Record<string, string> = {}
  for (const [key, entry] of Object.entries(value)) {
    if (typeof entry !== 'string') continue
    const name = key.trim()
    const headerValue = entry.trim()
    if (name && headerValue) headers[name] = headerValue
  }
  return Object.keys(headers).length > 0 ? headers : undefined
}

function getModelsField(item: Record<string, unknown>): EndpointModelMapping[] | undefined {
  const value = item.models || item.Models
  if (!Array.isArray(value)) return undefined
  const models = value.filter(isRecord).map((entry) => {
    const name = getStringField(entry, ['name', 'Name'])
    const alias = getStringField(entry, ['alias', 'Alias'])
    if (!name && !alias) return null
    return { name, alias }
  }).filter((entry): entry is EndpointModelMapping => entry !== null)
  return models.length > 0 ? models : undefined
}

function parseImportItems(payload: unknown): Array<Record<string, unknown>> {
  if (Array.isArray(payload)) return payload.filter(isRecord)
  if (!isRecord(payload)) return []

  const endpoints = payload.endpoints
  if (Array.isArray(endpoints)) return endpoints.filter(isRecord)

  return []
}

function removeEmptyFields(dto: EndpointImportInput): void {
  const fields = Object.entries(dto) as Array<[keyof EndpointImportInput, EndpointImportInput[keyof EndpointImportInput]]>
  for (const [key, value] of fields) {
    if (value === '' || value === undefined || value === null) {
      delete dto[key]
    }
  }
}

export function buildEndpointImportDTOs(rawJsonText: string): BuildEndpointImportResult {
  let payload: unknown
  try {
    payload = JSON.parse(rawJsonText)
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error)
    return { dtos: [], errors: [`JSON 解析失败：${message}`] }
  }

  const items = parseImportItems(payload)
  if (items.length === 0) {
    return { dtos: [], errors: ['请提供 endpoint JSON 数组'] }
  }

  const dtos: EndpointImportInput[] = []
  const errors: string[] = []
  const seen = new Set<string>()

  items.forEach((item, index) => {
    const name = getStringField(item, ['name', 'Name'])
    const apiUrl = getStringField(item, ['apiUrl', 'api_url', 'APIURL', 'ApiUrl'])
    const apiKey = getStringField(item, ['apiKey', 'api_key', 'APIKey', 'ApiKey'])
    const interfaceType = getStringField(item, ['interfaceType', 'interface_type', 'InterfaceType'])
    if (!name) errors.push(`#${index + 1}: missing name`)
    if (!apiUrl) errors.push(`#${index + 1}: missing apiUrl`)
    if (!apiKey) errors.push(`#${index + 1}: missing apiKey`)
    if (!interfaceType) errors.push(`#${index + 1}: missing interfaceType`)
    if (!name || !apiUrl || !apiKey || !interfaceType) return

    const localKey = `${interfaceType.toLowerCase()}\x00${apiUrl.toLowerCase()}\x00${apiKey}`
    if (seen.has(localKey)) {
      errors.push(`#${index + 1}: duplicate endpoint`)
      return
    }
    seen.add(localKey)

    const dto: EndpointImportInput = {
      name,
      apiUrl,
      apiKey,
      active: getBooleanField(item, ['active', 'Active']),
      enabled: getBooleanField(item, ['enabled', 'Enabled']),
      interfaceType,
      providerName: getStringField(item, ['providerName', 'provider_name', 'ProviderName']),
      model: getStringField(item, ['model', 'Model']),
      transformer: getStringField(item, ['transformer', 'Transformer']),
      proxyUrl: getStringField(item, ['proxyUrl', 'proxy_url', 'ProxyURL']),
      routes: getStringArrayField(item, ['routes', 'Routes']),
      models: getModelsField(item),
      headers: getHeadersField(item),
      remark: getStringField(item, ['remark', 'Remark']),
      priority: getNumberField(item, ['priority', 'Priority']),
    }

    removeEmptyFields(dto)
    dtos.push(dto)
  })

  return { dtos, errors }
}
