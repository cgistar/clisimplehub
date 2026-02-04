/**
 * 加密工具函数 - 用于 OAuth 2.0 PKCE 流程
 */

/**
 * 生成随机字符串（用于 code_verifier）
 * @param {number} length - 字符串长度（43-128）
 * @returns {string} 随机字符串
 */
export function generateRandomString(length) {
  const charset = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~'
  const randomValues = new Uint8Array(length)
  crypto.getRandomValues(randomValues)
  return Array.from(randomValues)
    .map((v) => charset[v % charset.length])
    .join('')
}

/**
 * SHA256 哈希
 * @param {string} plain - 明文字符串
 * @returns {Promise<ArrayBuffer>} 哈希结果
 */
async function sha256(plain) {
  const encoder = new TextEncoder()
  const data = encoder.encode(plain)
  const hash = await crypto.subtle.digest('SHA-256', data)
  return hash
}

/**
 * Base64URL 编码
 * @param {ArrayBuffer} arrayBuffer - 要编码的数据
 * @returns {string} Base64URL 编码的字符串
 */
function base64urlEncode(arrayBuffer) {
  const bytes = new Uint8Array(arrayBuffer)
  let binary = ''
  for (let i = 0; i < bytes.byteLength; i++) {
    binary += String.fromCharCode(bytes[i])
  }
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=/g, '')
}

/**
 * 生成 code_challenge（PKCE）
 * @param {string} verifier - code_verifier
 * @returns {Promise<string>} code_challenge
 */
export async function generateCodeChallenge(verifier) {
  const hashed = await sha256(verifier)
  return base64urlEncode(hashed)
}

/**
 * 生成 UUID（用于 state）
 * @returns {string} UUID
 */
export function generateUUID() {
  return crypto.randomUUID()
}
