import React, { useState, useEffect } from 'react'
import { KeyRound, Plus, Save, ShieldCheck, SlidersHorizontal, Trash2 } from 'lucide-react'
import { toast } from './Toast'
import { updateAccessKeys, updateModelSystemPrompts, updateKeyFailurePolicy, updateChannelModelFailurePolicy } from '../api'

function maskKeyLocal(key) {
  if (!key || key.length <= 8) return '****'
  return key.slice(0, 4) + '****' + key.slice(-4)
}

export default function SettingsPage({ settings, refreshSettings }) {
  const [accessKeys, setAccessKeys] = useState([])
  const [prompts, setPrompts] = useState({})
  const [keyThreshold, setKeyThreshold] = useState(3)
  const [keyCooldown, setKeyCooldown] = useState(600)
  const [modelThreshold, setModelThreshold] = useState(3)
  const [savingAK, setSavingAK] = useState(false)
  const [savingPrompt, setSavingPrompt] = useState(false)
  const [savingKF, setSavingKF] = useState(false)
  const [savingMF, setSavingMF] = useState(false)
  const [promptModel, setPromptModel] = useState('')
  const [promptText, setPromptText] = useState('')
  const [modelSearch, setModelSearch] = useState({})

  const models = (settings?.models || []).map(m => m.id || m).sort()

  useEffect(() => {
    if (!settings) return
    const keys = (settings.access_keys || []).map(k => {
      if (typeof k === 'string') return { key: k, allowed_models: [], excluded_models: [], expires_at: '' }
      return { key: k.key || '', allowed_models: k.allowed_models || [], excluded_models: k.excluded_models || [], expires_at: k.expires_at || '' }
    })
    setAccessKeys(keys)
    setPrompts(settings.model_system_prompts || {})
    setKeyThreshold(settings.key_failure_policy?.threshold || 3)
    setKeyCooldown(settings.key_failure_policy?.cooldown_seconds || 600)
    setModelThreshold(settings.channel_model_failure_policy?.threshold || 3)
  }, [settings])

  const handleSaveAccessKeys = async () => {
    setSavingAK(true)
    try {
      const list = accessKeys
        .map(k => ({
          key: k.key.trim(),
          allowed_models: k.allowed_models,
          excluded_models: k.excluded_models,
          expires_at: k.expires_at || '',
        }))
        .filter(k => k.key)
      await updateAccessKeys(list)
      toast('Access Keys 已保存')
      await refreshSettings()
    } catch (e) {
      toast('保存失败: ' + e.message, true)
    } finally {
      setSavingAK(false)
    }
  }

  const addAccessKey = () => {
    const bytes = new Uint8Array(24)
    crypto.getRandomValues(bytes)
    const key = 'sk-' + Array.from(bytes, b => b.toString(16).padStart(2, '0')).join('')
    setAccessKeys(prev => [...prev, { key, allowed_models: [], excluded_models: [], expires_at: '' }])
  }

  const removeAccessKey = (i) => setAccessKeys(prev => prev.filter((_, idx) => idx !== i))

  const updateAccessKey = (i, field, value) => {
    setAccessKeys(prev => prev.map((k, idx) => idx === i ? { ...k, [field]: value } : k))
  }

  const toggleAccessKeyModel = (i, field, model) => {
    setAccessKeys(prev => prev.map((k, idx) => {
      if (idx !== i) return k
      const list = k[field] || []
      return {
        ...k,
        [field]: list.includes(model) ? list.filter(m => m !== model) : [...list, model],
      }
    }))
  }

  const handleSavePrompts = async () => {
    setSavingPrompt(true)
    try {
      const clean = {}
      for (const [m, p] of Object.entries(prompts)) {
        if (p.trim()) clean[m] = p.trim()
      }
      await updateModelSystemPrompts(clean)
      toast('模型注入已保存')
      await refreshSettings()
    } catch (e) {
      toast('保存失败: ' + e.message, true)
    } finally {
      setSavingPrompt(false)
      setPromptModel('')
      setPromptText('')
    }
  }

  const addPromptFromForm = () => {
    if (!promptModel || !promptText.trim()) {
      toast('模型和 Prompt 不能为空', true)
      return
    }
    setPrompts(prev => ({ ...prev, [promptModel]: promptText.trim() }))
  }

  const removePrompt = (model) => {
    if (!window.confirm(`删除 ${model} 的注入？`)) return
    setPrompts(prev => {
      const next = { ...prev }
      delete next[model]
      return next
    })
  }

  const handleSaveKF = async () => {
    setSavingKF(true)
    try {
      await updateKeyFailurePolicy(keyThreshold, keyCooldown)
      toast('Key 熔断策略已保存')
      await refreshSettings()
    } catch (e) {
      toast('保存失败: ' + e.message, true)
    } finally {
      setSavingKF(false)
    }
  }

  const handleSaveMF = async () => {
    setSavingMF(true)
    try {
      await updateChannelModelFailurePolicy(modelThreshold)
      toast('模型阈值已保存')
      await refreshSettings()
    } catch (e) {
      toast('保存失败: ' + e.message, true)
    } finally {
      setSavingMF(false)
    }
  }

  return (
    <div className="space-y-6">
      <section className="grid gap-4">
        <div className="grid gap-3 sm:grid-cols-3">
          {[
            ['Admin Key', maskKeyLocal(settings?.admin_key || '')],
            ['Access Key', accessKeys.length.toLocaleString()],
            ['模型注入', Object.keys(prompts).length.toLocaleString()],
          ].map(([label, value]) => (
            <div key={label} className="card p-4">
              <div className="stat-label">{label}</div>
              <div className="mt-1 break-all font-mono text-lg font-semibold text-slate-950 dark:text-slate-50">{value}</div>
            </div>
          ))}
        </div>
      </section>

      <section className="grid gap-6 xl:grid-cols-[minmax(0,1.1fr)_minmax(22rem,0.9fr)]">
        <div className="card overflow-hidden">
          <div className="flex flex-wrap items-center justify-between gap-3 border-b px-5 py-4">
            <div className="flex items-center gap-2">
              <KeyRound className="h-4 w-4 text-blue-600 dark:text-blue-300" />
              <div>
                <h2 className="section-title">Access Key</h2>
                <p className="section-subtitle mt-1">客户端入口凭据和模型权限</p>
              </div>
            </div>
            <div className="flex gap-2">
              <button onClick={addAccessKey} className="btn-secondary btn-sm">
                <Plus className="h-4 w-4" />
                添加
              </button>
              <button onClick={handleSaveAccessKeys} disabled={savingAK} className="btn-primary btn-sm">
                <Save className="h-4 w-4" />
                {savingAK ? '保存中...' : '保存'}
              </button>
            </div>
          </div>
          <div className="max-h-[48rem] space-y-4 overflow-y-auto p-5 scroll-thin">
            {accessKeys.length === 0 ? (
              <div className="rounded-lg border border-dashed p-8 text-center text-sm text-slate-400">还没有 Access Key</div>
            ) : accessKeys.map((ak, i) => (
              <div key={i} className="rounded-lg border bg-slate-50/70 p-4 dark:bg-slate-950/50">
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <div>
                    <div className="font-mono text-xs font-semibold text-slate-500 dark:text-slate-400">#{i + 1} {maskKeyLocal(ak.key)}</div>
                    <div className="mt-1 text-xs text-slate-500 dark:text-slate-400">
                      白名单 {(ak.allowed_models || []).length || '全部'} / 黑名单 {(ak.excluded_models || []).length || '无'}
                    </div>
                  </div>
                  <button onClick={() => removeAccessKey(i)} className="btn-secondary btn-xs text-rose-600 dark:text-rose-400">
                    <Trash2 className="h-3.5 w-3.5" />
                    删除
                  </button>
                </div>
                <div className="mt-4 grid gap-3 md:grid-cols-[1fr_auto]">
                  <div className="space-y-1">
                    <div className="text-[10px] font-semibold text-slate-500 dark:text-slate-400">凭据密钥 Access Key</div>
                    <input className="input input-sm font-mono" value={ak.key} onChange={e => updateAccessKey(i, 'key', e.target.value)} placeholder="sk-..." />
                  </div>
                  <div className="space-y-1">
                    <div className="flex items-center justify-between gap-3 text-[10px] font-semibold text-slate-500 dark:text-slate-400">
                      <span>过期时间 Expires At</span>
                      <div className="flex gap-1 text-[9px] font-semibold text-indigo-600 dark:text-indigo-400">
                        <button
                          type="button"
                          onClick={() => {
                            const d = new Date()
                            d.setDate(d.getDate() + 1)
                            updateAccessKey(i, 'expires_at', d.toISOString())
                          }}
                        >
                          1天
                        </button>
                        <span>·</span>
                        <button
                          type="button"
                          onClick={() => {
                            const d = new Date()
                            d.setMonth(d.getMonth() + 1)
                            updateAccessKey(i, 'expires_at', d.toISOString())
                          }}
                        >
                          1月
                        </button>
                        <span>·</span>
                        <button
                          type="button"
                          onClick={() => {
                            updateAccessKey(i, 'expires_at', '')
                          }}
                        >
                          永久
                        </button>
                      </div>
                    </div>
                    <input className="input input-sm font-mono text-xs w-full sm:w-52" type="datetime-local" value={ak.expires_at ? ak.expires_at.slice(0, 16) : ''} onChange={e => updateAccessKey(i, 'expires_at', e.target.value ? new Date(e.target.value).toISOString() : '')} />
                  </div>
                </div>
                <div className="mt-4 grid gap-4 md:grid-cols-2">
                  {[
                    ['allowed_models', '白名单 (允许列表)', '全部可用', 'blue'],
                    ['excluded_models', '黑名单 (排除列表)', '无排除', 'rose'],
                  ].map(([field, label, empty, tone]) => {
                    const queryKey = `${i}-${field}`
                    const query = modelSearch[queryKey] || ''
                    const filteredModels = models.filter(m => m.toLowerCase().includes(query.toLowerCase()))
                    return (
                      <div key={field} className="flex flex-col">
                        <div className="mb-2 flex items-center justify-between gap-2">
                          <div className="text-xs font-semibold text-slate-500 dark:text-slate-400">{label}</div>
                          <input
                            type="text"
                            className="input input-xs w-28 py-0.5 px-1.5"
                            placeholder="过滤模型..."
                            value={query}
                            onChange={e => setModelSearch(prev => ({ ...prev, [queryKey]: e.target.value }))}
                          />
                        </div>
                        <div className="flex gap-2 text-[9px] font-semibold text-indigo-600 dark:text-indigo-400 mb-1.5">
                          <button
                            type="button"
                            onClick={() => {
                              const newModels = Array.from(new Set([...(ak[field] || []), ...filteredModels]))
                              updateAccessKey(i, field, newModels)
                            }}
                          >
                            全选当前
                          </button>
                          <span className="text-slate-300 dark:text-slate-700">|</span>
                          <button
                            type="button"
                            onClick={() => {
                              const newModels = (ak[field] || []).filter(m => !filteredModels.includes(m))
                              updateAccessKey(i, field, newModels)
                            }}
                          >
                            清除当前
                          </button>
                        </div>
                        <div className="max-h-28 overflow-y-auto rounded-lg border bg-white p-2 dark:bg-slate-900 scroll-thin flex-1">
                          {filteredModels.length === 0 && <div className="mb-2 text-[10px] text-slate-400">{empty}</div>}
                          <div className="flex flex-wrap gap-1.5">
                            {filteredModels.map(model => {
                              const checked = (ak[field] || []).includes(model)
                              const cls = checked
                                ? tone === 'blue'
                                  ? 'border-blue-350 bg-blue-50/70 text-blue-600 dark:border-blue-600/70 dark:bg-blue-950/20 dark:text-blue-300'
                                  : 'border-rose-350 bg-rose-50/70 text-rose-700 dark:border-rose-800/70 dark:bg-rose-950/20 dark:text-rose-300'
                                : 'border-slate-200/80 text-slate-500 hover:border-slate-350 dark:border-slate-800 dark:text-slate-400'
                              return (
                                <label key={model} className={`inline-flex cursor-pointer items-center rounded-md border px-1.5 py-0.5 text-[10px] font-mono transition-all ${cls}`}>
                                  <input type="checkbox" className="sr-only" checked={checked} onChange={() => toggleAccessKeyModel(i, field, model)} />
                                  {model}
                                </label>
                              )
                            })}
                          </div>
                        </div>
                      </div>
                    )
                  })}
                </div>
              </div>
            ))}
          </div>
        </div>

        <div className="space-y-6">
          <div className="card overflow-hidden">
            <div className="flex items-center justify-between border-b px-5 py-4">
              <div>
                <h2 className="section-title">模型注入</h2>
                <p className="section-subtitle mt-1">按模型覆写 System Prompt</p>
              </div>
              <button onClick={handleSavePrompts} disabled={savingPrompt} className="btn-primary btn-sm">
                <Save className="h-4 w-4" />
                {savingPrompt ? '保存中...' : '保存全部'}
              </button>
            </div>
            <div className="p-5">
              <div className="grid gap-2 sm:grid-cols-[1fr_auto]">
                <select className="select select-sm" value={promptModel} onChange={e => { setPromptModel(e.target.value); setPromptText(prompts[e.target.value] || '') }}>
                  <option value="">选择模型</option>
                  {models.map(m => <option key={m} value={m}>{m}</option>)}
                </select>
                <button onClick={addPromptFromForm} className="btn-secondary btn-sm">
                  <Plus className="h-4 w-4" />
                  添加
                </button>
              </div>
              {promptModel && (
                <textarea className="input mt-3 min-h-[8rem] font-mono text-xs" placeholder="System Prompt..." value={promptText} onChange={e => setPromptText(e.target.value)} />
              )}

              <div className="mt-4 max-h-72 space-y-2 overflow-y-auto scroll-thin">
                {Object.entries(prompts).length === 0 ? (
                  <div className="rounded-lg border border-dashed p-6 text-center text-sm text-slate-400">暂无注入</div>
                ) : Object.entries(prompts).sort(([a], [b]) => a.localeCompare(b)).map(([model, prompt]) => (
                  <div key={model} className="rounded-lg border bg-slate-50/70 p-3 dark:bg-slate-950/50">
                    <div className="flex items-start justify-between gap-3">
                      <div className="min-w-0 flex-1">
                        <div className="truncate font-mono text-xs font-semibold text-slate-900 dark:text-slate-100">{model}</div>
                        <div className="mt-1 truncate text-xs text-slate-500 dark:text-slate-400">{prompt}</div>
                      </div>
                      <div className="flex shrink-0 gap-1">
                        <button onClick={() => { setPromptModel(model); setPromptText(prompt) }} className="btn-ghost btn-xs">编辑</button>
                        <button onClick={() => removePrompt(model)} className="btn-ghost btn-xs text-rose-600 dark:text-rose-400">删除</button>
                      </div>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          </div>

          <div className="card overflow-hidden">
            <div className="flex items-center justify-between border-b px-5 py-4">
              <div className="flex items-center gap-2">
                <SlidersHorizontal className="h-4 w-4 text-blue-600 dark:text-blue-300" />
                <div>
                  <h2 className="section-title">熔断策略</h2>
                  <p className="section-subtitle mt-1">失败后怎么暂停和恢复</p>
                </div>
              </div>
            </div>
            <div className="space-y-5 p-5">
              <div className="grid gap-4 sm:grid-cols-2">
                <label>
                  <span className="mb-1 block text-xs font-semibold text-slate-500 dark:text-slate-400">Key 连续失败次数</span>
                  <input className="input" type="number" value={keyThreshold} onChange={e => setKeyThreshold(Number(e.target.value))} min={1} />
                </label>
                <label>
                  <span className="mb-1 block text-xs font-semibold text-slate-500 dark:text-slate-400">冷却时间（秒）</span>
                  <input className="input" type="number" value={keyCooldown} onChange={e => setKeyCooldown(Number(e.target.value))} min={1} />
                </label>
              </div>
              <button onClick={handleSaveKF} disabled={savingKF} className="btn-primary w-full">
                <Save className="h-4 w-4" />
                {savingKF ? '保存中...' : '保存 Key 策略'}
              </button>
              <div className="border-t pt-5">
                <label>
                  <span className="mb-1 block text-xs font-semibold text-slate-500 dark:text-slate-400">渠道模型失败阈值</span>
                  <input className="input" type="number" value={modelThreshold} onChange={e => setModelThreshold(Number(e.target.value))} min={1} />
                </label>
                <button onClick={handleSaveMF} disabled={savingMF} className="btn-secondary mt-3 w-full">
                  <Save className="h-4 w-4" />
                  {savingMF ? '保存中...' : '保存模型阈值'}
                </button>
              </div>
            </div>
          </div>
        </div>
      </section>
    </div>
  )
}
