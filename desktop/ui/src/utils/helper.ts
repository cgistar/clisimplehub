export async function waitForWails(maxAttempts = 50, intervalMs = 100): Promise<void> {
  let attempts = 0
  while (!window.go && attempts < maxAttempts) {
    await new Promise((resolve) => setTimeout(resolve, intervalMs))
    attempts++
  }
}

export function generateRandomString(length: number): string {
  const charset = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~'
  const randomValues = new Uint8Array(length)
  crypto.getRandomValues(randomValues)
  return Array.from(randomValues)
    .map((v) => charset[v % charset.length])
    .join('')
}

async function sha256(plain: string): Promise<ArrayBuffer> {
  const encoder = new TextEncoder()
  const data = encoder.encode(plain)
  return crypto.subtle.digest('SHA-256', data)
}

function base64urlEncode(arrayBuffer: ArrayBuffer): string {
  const bytes = new Uint8Array(arrayBuffer)
  let binary = ''
  for (let i = 0; i < bytes.byteLength; i++) {
    binary += String.fromCharCode(bytes[i])
  }
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=/g, '')
}

export async function generateCodeChallenge(verifier: string): Promise<string> {
  const hashed = await sha256(verifier)
  return base64urlEncode(hashed)
}

export function generateUUID(): string {
  return crypto.randomUUID()
}
