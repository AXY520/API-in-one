import React, { useState } from 'react'
import { KeyRound, ShieldCheck } from 'lucide-react'
import { toast } from './Toast'

export default function LoginPage({ onLogin }) {
  const [key, setKey] = useState('')
  const [busy, setBusy] = useState(false)

  const handleSubmit = async (e) => {
    e.preventDefault()
    if (!key.trim()) return
    setBusy(true)
    try {
      await onLogin(key.trim())
    } catch (err) {
      toast('连接校验失败: ' + err.message, true)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="app-shell flex min-h-screen items-center justify-center p-4 relative overflow-hidden">
      {/* Decorative background glows */}
      <div className="absolute top-1/4 left-1/3 w-72 h-72 bg-indigo-500/10 rounded-full blur-3xl pointer-events-none" />
      <div className="absolute bottom-1/4 right-1/3 w-80 h-80 bg-violet-500/10 rounded-full blur-3xl pointer-events-none" />

      <div className="w-full max-w-md z-10">
        <div className="card p-8 border border-white/20 dark:border-slate-800/50 bg-white/60 dark:bg-slate-900/60 shadow-xl backdrop-blur-2xl">
          <div className="mb-8 text-center flex flex-col items-center">
            <div className="flex h-16 w-16 items-center justify-center rounded-2xl bg-gradient-to-tr from-indigo-600 to-violet-500 text-white shadow-lg shadow-indigo-600/30 hover:scale-105 transition-transform duration-300">
              <ShieldCheck className="h-8 w-8" />
            </div>
            <h2 className="mt-6 text-2xl font-extrabold tracking-tight bg-gradient-to-r from-slate-950 to-indigo-950 dark:from-slate-50 dark:to-indigo-50 bg-clip-text text-transparent">
              API-in-one 网关管理
            </h2>
            <p className="mt-2 text-xs text-slate-500 dark:text-slate-400">
              统一多路 LLM 供应商的多协议 API 网关
            </p>
          </div>
          <form onSubmit={handleSubmit} className="space-y-5">
            <div>
              <label className="mb-2 block text-xs font-semibold uppercase tracking-wider text-slate-500 dark:text-slate-400">
                管理员密钥 Admin Key
              </label>
              <div className="relative">
                <input
                  className="input font-mono pr-10"
                  type="password"
                  placeholder="输入 Admin Key"
                  value={key}
                  onChange={e => setKey(e.target.value)}
                  autoFocus
                />
                <KeyRound className="absolute right-3 top-1/2 -translate-y-1/2 h-4 w-4 text-slate-400 pointer-events-none" />
              </div>
            </div>
            <button type="submit" className="btn-primary w-full py-2.5 mt-2" disabled={busy}>
              {busy ? (
                <span className="inline-block w-5 h-5 border-2 border-white/30 border-t-white rounded-full animate-spin" />
              ) : (
                <>
                  <KeyRound className="h-4 w-4" />
                  验证并登入
                </>
              )}
            </button>
          </form>
        </div>
      </div>
    </div>
  )
}

