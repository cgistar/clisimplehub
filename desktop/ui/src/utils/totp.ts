function normalizeSecret(input: string): string {
  const raw = String(input || '').trim()
  if (!raw) return ''

  if (raw.toLowerCase().startsWith('otpauth://')) {
    try {
      const url = new URL(raw)
      return String(url.searchParams.get('secret') || '').trim().replace(/\s+/g, '').toUpperCase()
    } catch {
      return ''
    }
  }

  return raw.replace(/\s+/g, '').toUpperCase()
}

function decodeBase32(input: string): Uint8Array | null {
  const secret = normalizeSecret(input)
  if (!secret) return null

  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'
  let bits = 0
  let value = 0
  const bytes: number[] = []

  for (const char of secret) {
    if (char === '=') continue
    const index = alphabet.indexOf(char)
    if (index < 0) return null

    value = (value << 5) | index
    bits += 5

    if (bits >= 8) {
      bytes.push((value >>> (bits - 8)) & 0xff)
      bits -= 8
    }
  }

  return bytes.length > 0 ? new Uint8Array(bytes) : null
}

async function hmacSha1(secret: Uint8Array, counter: bigint): Promise<Uint8Array> {
  const buffer = new ArrayBuffer(8)
  const view = new DataView(buffer)
  view.setBigUint64(0, counter, false)

  const key = await crypto.subtle.importKey(
    'raw',
    secret,
    { name: 'HMAC', hash: 'SHA-1' },
    false,
    ['sign']
  )
  const signature = await crypto.subtle.sign('HMAC', key, buffer)
  return new Uint8Array(signature)
}

export async function generateTotpCode(secretInput: string, nowMs = Date.now()): Promise<string> {
  const secret = decodeBase32(secretInput)
  if (!secret || typeof crypto === 'undefined' || !crypto.subtle) {
    return ''
  }

  const counter = BigInt(Math.floor(nowMs / 1000 / 30))
  const digest = await hmacSha1(secret, counter)
  const offset = digest[digest.length - 1] & 0x0f
  const binary =
    ((digest[offset] & 0x7f) << 24) |
    ((digest[offset + 1] & 0xff) << 16) |
    ((digest[offset + 2] & 0xff) << 8) |
    (digest[offset + 3] & 0xff)

  return String(binary % 1000000).padStart(6, '0')
}
