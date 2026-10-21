import { useEffect, useRef, useState, type FormEvent } from 'react'
import { toast } from 'sonner'
import { webApi } from '@/api/web'
import { Dialog, DialogBody, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import type { CodexOAuthResult } from '@/types'

interface CodexOAuthLoginDialogProps {
  open: boolean
  onClose: () => void
  onSuccess: () => void | Promise<void>
}

function toErrorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

export default function CodexOAuthLoginDialog({ open, onClose, onSuccess }: CodexOAuthLoginDialogProps) {
  const stateRef = useRef('')
  const requestGen = useRef(0)
  const finishedRef = useRef(false)
  const reportedErrorRef = useRef('')
  const [authUrl, setAuthUrl] = useState('')
  const [callbackUrl, setCallbackUrl] = useState('')
  const [callbackHost, setCallbackHost] = useState('127.0.0.1')
  const [callbackPort, setCallbackPort] = useState(1455)
  const [listening, setListening] = useState(false)
  const [starting, setStarting] = useState(false)
  const [submitting, setSubmitting] = useState(false)

  const sshCommand = `ssh -L ${callbackPort}:${callbackHost}:${callbackPort} <用户>@<服务器>`

  async function startLogin(): Promise<void> {
    const gen = ++requestGen.current
    const previous = stateRef.current
    stateRef.current = ''
    if (previous) void webApi.cancelCodexOAuthLogin(previous).catch(() => undefined)

    finishedRef.current = false
    reportedErrorRef.current = ''
    setStarting(true)
    setAuthUrl('')
    setCallbackUrl('')
    setListening(false)
    try {
      const result = await webApi.startCodexOAuthLogin()
      if (gen !== requestGen.current) {
        if (result.state) void webApi.cancelCodexOAuthLogin(result.state).catch(() => undefined)
        return
      }
      stateRef.current = result.state || ''
      setAuthUrl(result.url || '')
      setCallbackHost(result.callbackHost || '127.0.0.1')
      setCallbackPort(result.callbackPort || 1455)
      setListening(Boolean(result.listening))
    } catch (error) {
      if (gen === requestGen.current) toast.error(`生成登录链接失败：${toErrorMessage(error)}`)
    } finally {
      if (gen === requestGen.current) {
        setStarting(false)
      }
    }
  }

  useEffect(() => {
    if (!open) return
    void startLogin()
    return () => {
      requestGen.current += 1
      const state = stateRef.current
      stateRef.current = ''
      setAuthUrl('')
      setCallbackUrl('')
      setListening(false)
      if (state) void webApi.cancelCodexOAuthLogin(state).catch((error) => {
        toast.error(`停止回调监听失败：${toErrorMessage(error)}`)
      })
    }
    // 每次打开都生成新链接；旧 state/PKCE 由后端保留至超时。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  useEffect(() => {
    if (!open) return
    let stopped = false
    const timer = window.setInterval(() => {
      const state = stateRef.current
      if (!state || stopped || finishedRef.current) return
      void webApi.codexOAuthLoginStatus(state).then((status) => {
        if (stopped || finishedRef.current || stateRef.current !== state) return
        setListening(Boolean(status.listening))
        if (status.callbackHost) setCallbackHost(status.callbackHost)
        if (status.callbackPort) setCallbackPort(status.callbackPort)
        if (status.status === 'success') {
          void finishLogin(status)
          return
        }
        if (status.error && reportedErrorRef.current !== status.error) {
          reportedErrorRef.current = status.error
          toast.error(status.error)
        }
      }).catch(() => undefined)
    }, 2000)
    return () => {
      stopped = true
      window.clearInterval(timer)
    }
    // finishLogin 只在打开期间轮询使用。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  function openAuthUrl(): void {
    if (!authUrl) return
    window.open(authUrl, '_blank', 'noopener,noreferrer')
  }

  async function finishLogin(result: CodexOAuthResult): Promise<void> {
    if (finishedRef.current) return
    finishedRef.current = true
    stateRef.current = ''
    const email = result.email ? `：${result.email}` : ''
    toast.success((result.message || 'OpenAI 登录成功') + email)
    await onSuccess()
    onClose()
  }

  async function copyText(value: string, success: string): Promise<void> {
    if (!value) return
    try {
      await navigator.clipboard.writeText(value)
      toast.success(success)
    } catch (error) {
      toast.error(`复制失败：${toErrorMessage(error)}`)
    }
  }

  async function submitCallback(event: FormEvent): Promise<void> {
    event.preventDefault()
    const redirectUrl = callbackUrl.trim()
    if (!redirectUrl || submitting || finishedRef.current) return

    setSubmitting(true)
    try {
      await finishLogin(await webApi.completeCodexOAuthLogin(redirectUrl))
    } catch (error) {
      reportedErrorRef.current = toErrorMessage(error)
      toast.error(reportedErrorRef.current)
    } finally {
      setSubmitting(false)
    }
  }

  const busy = starting || submitting

  return (
    <Dialog open={open} onOpenChange={(nextOpen) => { if (!nextOpen && !submitting) onClose() }}>
      <DialogContent closeDisabled={submitting}>
        <DialogHeader>
          <div>
            <DialogTitle>OpenAI OAuth 登录</DialogTitle>
            <DialogDescription>生成登录链接。服务器会临时监听 127.0.0.1:1455，可用 SSH 转发，也可以粘贴回调地址</DialogDescription>
          </div>
        </DialogHeader>

        <form onSubmit={(event) => void submitCallback(event)}>
          <DialogBody>
            <div className="field">
              <label className="field-label" htmlFor="codex-oauth-url">登录链接</label>
              <div className="inline-field-action">
                <input
                  id="codex-oauth-url"
                  className="input"
                  value={starting ? '正在生成登录链接...' : authUrl}
                  readOnly
                  spellCheck={false}
                  onFocus={(event) => event.currentTarget.select()}
                />
                <button className="btn" type="button" onClick={() => void copyText(authUrl, '登录链接已复制')} disabled={!authUrl || busy}>复制</button>
                <button className="btn" type="button" onClick={openAuthUrl} disabled={!authUrl || busy}>打开</button>
              </div>
            </div>

            <div className="field mt-14">
              <label className="field-label" htmlFor="codex-oauth-callback">Callback URL</label>
              <input
                id="codex-oauth-callback"
                className="input"
                value={callbackUrl}
                onChange={(event) => setCallbackUrl(event.target.value)}
                placeholder="远程浏览器登录后，将地址栏中的 localhost 回调 URL 粘贴到这里"
                spellCheck={false}
                autoComplete="off"
                disabled={submitting}
              />
            </div>

            <div className="field mt-14">
              <label className="field-label" htmlFor="codex-oauth-ssh">SSH 本地转发</label>
              <div className="inline-field-action">
                <input id="codex-oauth-ssh" className="input" value={sshCommand} readOnly spellCheck={false} onFocus={(event) => event.currentTarget.select()} />
                <button className="btn" type="button" onClick={() => void copyText(sshCommand, 'SSH 命令已复制')} disabled={busy}>复制</button>
              </div>
            </div>

            <div className="notice mt-14">
              {listening ? `1455 端口监听中（${callbackHost}:${callbackPort}）。` : '1455 端口未监听。'}
              本机先执行上面的 SSH 命令，再在本机浏览器打开登录链接，授权后会自动完成。也可以把地址栏里的 http://localhost:1455/auth/callback?code=...&state=... 粘贴回来。关闭此窗口、登录成功或超过 10 分钟都会关闭该端口。
            </div>
          </DialogBody>

          <DialogFooter>
            <button className="btn" type="button" onClick={() => void startLogin()} disabled={busy}>
              {starting ? '生成中...' : '重新生成'}
            </button>
            <button className="btn" type="button" onClick={onClose} disabled={submitting}>关闭并停止监听</button>
            <button className="btn primary" type="submit" disabled={busy || !callbackUrl.trim()}>
              {submitting ? '登录中...' : '提交回调'}
            </button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
