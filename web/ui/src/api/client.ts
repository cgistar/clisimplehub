export interface ApiError extends Error {
  status?: number
  payload?: unknown
}

export async function apiFetch<T>(path: string, options: RequestInit = {}): Promise<T> {
  const headers = { ...(options.headers || {}) } as Record<string, string>
  const isJsonBody = options.body && !(options.body instanceof FormData)
  if (isJsonBody && !headers['Content-Type']) {
    headers['Content-Type'] = 'application/json'
  }

  const response = await fetch(path, {
    credentials: 'same-origin',
    ...options,
    headers,
  })

  const text = await response.text()
  let data: unknown = null
  if (text) {
    try {
      data = JSON.parse(text) as T
    } catch {
      data = { raw: text }
    }
  }

  if (!response.ok) {
    const payload = data as { error?: string; message?: string }
    const message = payload?.error || payload?.message || `请求失败 (${response.status})`
    const error = new Error(message) as ApiError
    error.status = response.status
    error.payload = data
    throw error
  }

  return data as T
}
