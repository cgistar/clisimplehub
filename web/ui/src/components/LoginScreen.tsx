import type { FormEvent } from 'react'

interface LoginScreenProps {
  hasApiKey: boolean
  loginKey: string
  setLoginKey: (value: string) => void
  loading: boolean
  error: string
  onSubmit: (event: FormEvent<HTMLFormElement>) => void | Promise<void>
}

export default function LoginScreen({ hasApiKey, loginKey, setLoginKey, loading, error, onSubmit }: LoginScreenProps) {
  const inputDisabled = loading
  const submitDisabled = loading || (hasApiKey && !loginKey.trim())

  return (
    <div className="login-screen">
      <div className="login-card">
        <div className="brand">
          <div className="brand-badge">CSH</div>
          <div>
            <div className="brand-title">Cli Simple Hub Web</div>
            <div className="brand-subtitle">后台管理界面</div>
          </div>
        </div>

        {error ? <div className="error-banner">{error}</div> : null}

        <form onSubmit={onSubmit}>
          <div className="field">
            <label className="field-label">密钥</label>
            <input
              className="input"
              type="password"
              placeholder="请输入密钥"
              value={loginKey}
              disabled={inputDisabled}
              onChange={(event) => setLoginKey(event.target.value)}
            />
          </div>

          <div className="actions mt-18">
            <button type="submit" className="btn primary full" disabled={submitDisabled}>
              {loading ? '登录中...' : '登录'}
            </button>
          </div>
        </form>

        <div className="footer-note">
          copyright for brinfo.
        </div>
      </div>
    </div>
  )
}
