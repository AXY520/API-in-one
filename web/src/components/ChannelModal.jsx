import React, { useState, useEffect, useRef } from 'react'
import { toast } from './Toast'
import { createChannel, updateChannel, fetchChannelKeys, fetchModels, probeChannelKeys, updateChannelModelState } from '../api'
import KeyModal from './KeyModal'

function routeBaseURL(type, baseURL, baseURLClaude, baseURLGemini) {
  if (type === 'claude') return (baseURLClaude || baseURL).trim()
  if (type === 'gemini') return (baseURLGemini || baseURL).trim()
  return (baseURL || baseURLClaude || baseURLGemini).trim()
}

function probeProtocolForChannel(channel) {
  if (!channel) return ''
  if (channel.type === 'claude' || (!channel.base_url && channel.base_url_claude)) return 'claude'
  if (channel.type === 'gemini' || (!channel.base_url && channel.base_url_gemini)) return 'gemini'
  if (channel.responses_only) return 'responses'
  return channel.type || 'openai'
}

export default function ChannelModal({ channel, onClose, onSaved }) {
  const isEdit = !!channel
  const [name, setName] = useState(channel?.name || '')
  const [type, setType] = useState(channel?.type || 'openai')
  const [baseURL, setBaseURL] = useState(channel?.base_url || '')
  const [baseURLClaude, setBaseURLClaude] = useState(channel?.base_url_claude || '')
  const [baseURLGemini, setBaseURLGemini] = useState(channel?.base_url_gemini || '')
  const [supportsResponses, setSupportsResponses] = useState(channel?.supports_responses || false)
  const [disableMiMo, setDisableMiMo] = useState(channel?.disable_mimo_compat || false)
  const [channelModels, setChannelModels] = useState(channel?.models || [])
  const [disabledModels, setDisabledModels] = useState(channel?.disabled_models || [])
  const [priority, setPriority] = useState(channel?.priority || 10)
  const [weight, setWeight] = useState(channel?.weight || 100)
  const [enabled, setEnabled] = useState(channel?.enabled !== false)
  const [saving, setSaving] = useState(false)
  const [keyModalOpen, setKeyModalOpen] = useState(false)
  const [editorKey, setEditorKey] = useState({})
  const [modelMapping, setModelMapping] = useState(Object.entries(channel?.model_mapping || {}).map(([k, v]) => ({ alias: k, upstream: v })))
  const [fetchingModels, setFetchingModels] = useState(false)
  const [probingModel, setProbingModel] = useState(null)
  const [modelProbeResults, setModelProbeResults] = useState({})
  const [showModelPicker, setShowModelPicker] = useState(false)
  const [pendingModels, setPendingModels] = useState([])
  const [pickerSelection, setPickerSelection] = useState({})

  useEffect(() => {
    if (isEdit && channel?.name) {
      fetchChannelKeys(channel.name).then(data => {
        setEditorKey({ keys: data.keys || [], disabledKeys: data.disabled_keys || [], keyModels: data.key_models || {}, keyStats: channel.key_stats || [] })
      }).catch(() => {})
    }
  }, [isEdit, channel?.name])

  const addModel = (model) => {
    if (!channelModels.includes(model)) {
      setChannelModels(prev => [...prev, model])
    }
  }

  const removeModel = (model) => {
    setChannelModels(prev => prev.filter(m => m !== model))
    setDisabledModels(prev => prev.filter(m => m !== model))
    setModelMapping(prev => prev.filter(({ alias, upstream }) => alias !== model && upstream !== model))
  }

  const toggleDisableModel = (model) => {
    setDisabledModels(prev =>
      prev.includes(model) ? prev.filter(m => m !== model) : [...prev, model]
    )
  }

  const handleProbeModel = async (model) => {
    const chName = channel?.name || name.trim()
    if (!chName || !isEdit) {
      toast('请先保存渠道后再探活', true)
      return
    }
    setProbingModel(model)
    try {
      const result = await probeChannelKeys(chName, { model, protocol: probeProtocolForChannel(channel) })
      const probeResults = result.results || []
      const okCount = probeResults.filter(r => r.ok).length
      const failCount = probeResults.filter(r => !r.ok).length
      const ok = probeResults.length > 0 && okCount === probeResults.length
      setModelProbeResults(prev => ({ ...prev, [model]: { ok, okCount, failCount, total: probeResults.length, results: probeResults } }))
      toast(`探活 ${model}: ${ok ? `全部成功 (${okCount}/${probeResults.length})` : `${okCount}成功 ${failCount}失败`}`)
    } catch (e) {
      setModelProbeResults(prev => ({ ...prev, [model]: { ok: false, okCount: 0, failCount: 0, total: 0, error: e.message } }))
      toast(`探活 ${model} 失败: ${e.message}`, true)
    } finally {
      setProbingModel(null)
    }
  }

  const handleSave = async () => {
    if (!name.trim()) {
      toast('渠道名称必填', true)
      return
    }
    if (!baseURL.trim() && !baseURLClaude.trim() && !baseURLGemini.trim()) {
      toast('至少填写一个 Base URL', true)
      return
    }
    if (channelModels.length === 0) {
      toast('至少配置一个模型', true)
      return
    }
    setSaving(true)
    try {
      const mapping = {}
      modelMapping.forEach(({ alias, upstream }) => {
        if (alias && upstream) mapping[alias] = upstream
      })
      const body = {
        name: name.trim(),
        type,
        base_url: baseURL.trim(),
        base_url_claude: baseURLClaude.trim(),
        base_url_gemini: baseURLGemini.trim(),
        supports_responses: supportsResponses,
        disable_mimo_compat: disableMiMo,
        keys: editorKey.keys || [],
        disabled_keys: editorKey.disabledKeys || [],
        key_models: editorKey.keyModels || {},
        models: channelModels,
        disabled_models: disabledModels,
        model_mapping: mapping,
        priority,
        weight,
        enabled,
      }
      if (isEdit) {
        await updateChannel(channel.name, body)
        toast('渠道已更新')
      } else {
        await createChannel(body)
        toast('渠道已创建')
      }
      onSaved()
    } catch (e) {
      toast('保存失败: ' + e.message, true)
    } finally {
      setSaving(false)
    }
  }

  const handleFetchModels = async () => {
    const fetchBaseURL = routeBaseURL(type, baseURL, baseURLClaude, baseURLGemini)
    if (!fetchBaseURL) {
      toast('请先填写当前类型的 Base URL', true)
      return
    }
    let key = editorKey.keys?.[0] || ''
    if (!key && isEdit) {
      try {
        const data = await fetchChannelKeys(channel.name)
        key = data.keys?.[0] || ''
      } catch {}
    }
    if (!key) {
      toast('请先添加至少一个 API Key', true)
      return
    }
    setFetchingModels(true)
    try {
      const data = await fetchModels(fetchBaseURL, key, type)
      const list = data.models || []
      if (list.length === 0) {
        toast('上游模型列表为空', true)
        return
      }
      const initialSel = {}
      list.forEach(m => { initialSel[m] = channelModels.includes(m) })
      setPendingModels(list)
      setPickerSelection(initialSel)
      setShowModelPicker(true)
    } catch (e) {
      toast('拉取模型失败: ' + e.message, true)
    } finally {
      setFetchingModels(false)
    }
  }

  const confirmModelSelection = () => {
    const selected = pendingModels.filter(m => pickerSelection[m])
    const newlyAdded = selected.filter(m => !channelModels.includes(m))
    const removed = channelModels.filter(m => !pickerSelection[m] && pendingModels.includes(m))
    setChannelModels(prev => {
      const keep = prev.filter(m => !removed.includes(m))
      return [...keep, ...newlyAdded]
    })
    setModelMapping(prev => prev.filter(({ alias, upstream }) => !removed.includes(alias) && !removed.includes(upstream)))
    setDisabledModels(prev => prev.filter(m => !removed.includes(m)))
    setShowModelPicker(false)
    toast(`已选择 ${selected.length} 个模型`)
  }

  const togglePickerItem = (model) => {
    setPickerSelection(prev => ({ ...prev, [model]: !prev[model] }))
  }

  const addMapping = () => setModelMapping(prev => [...prev, { alias: '', upstream: '' }])
  const updateMapping = (i, field, value) => {
    setModelMapping(prev => prev.map((m, idx) => idx === i ? { ...m, [field]: value } : m))
  }
  const removeMapping = (i) => setModelMapping(prev => prev.filter((_, idx) => idx !== i))

  const handleKeySaved = (data) => {
    setEditorKey(data)
    setKeyModalOpen(false)
  }

  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal-content max-w-3xl" onClick={e => e.stopPropagation()}>
        <div className="sticky top-0 bg-white dark:bg-gray-900 border-b border-gray-200 dark:border-gray-800 px-6 py-4 flex items-center justify-between z-10">
          <div>
            <div className="text-[10px] font-semibold uppercase text-gray-400 tracking-wider">Channel Editor</div>
            <h2 className="text-lg font-bold text-gray-900 dark:text-gray-100">{isEdit ? `编辑渠道 — ${channel.name}` : '添加新渠道'}</h2>
          </div>
          <button onClick={onClose} className="btn-ghost p-1">
            <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" /></svg>
          </button>
        </div>

        <div className="p-6 space-y-6 max-h-[calc(100vh-10rem)] overflow-y-auto scroll-thin">
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <div>
              <label className="block text-xs font-semibold text-gray-500 dark:text-gray-400 mb-1">渠道名称 *</label>
              <input className="input" value={name} onChange={e => setName(e.target.value)} placeholder="例如: azure-openai-west" disabled={isEdit} />
            </div>
            <div>
              <label className="block text-xs font-semibold text-gray-500 dark:text-gray-400 mb-1">渠道类型</label>
              <select className="select" value={type} onChange={e => setType(e.target.value)}>
                <option value="openai">OpenAI</option>
                <option value="claude">Claude</option>
                <option value="gemini">Gemini</option>
              </select>
            </div>
            <div className="md:col-span-2">
              <label className="block text-xs font-semibold text-gray-500 dark:text-gray-400 mb-1">标准 Base URL</label>
              <input className="input font-mono" value={baseURL} onChange={e => setBaseURL(e.target.value)} placeholder="https://api.openai.com/v1" />
            </div>
            <div>
              <label className="block text-xs font-semibold text-gray-500 dark:text-gray-400 mb-1">Claude 专有 URL</label>
              <input className="input font-mono" value={baseURLClaude} onChange={e => setBaseURLClaude(e.target.value)} placeholder="可选，覆盖 Claude 转发地址" />
            </div>
            <div>
              <label className="block text-xs font-semibold text-gray-500 dark:text-gray-400 mb-1">Gemini 专有 URL</label>
              <input className="input font-mono" value={baseURLGemini} onChange={e => setBaseURLGemini(e.target.value)} placeholder="可选，覆盖 Gemini 转发地址" />
            </div>
          </div>

          <div className="flex items-center gap-6">
            <label className="flex items-center gap-2 cursor-pointer">
              <input type="checkbox" className="w-4 h-4 rounded border-gray-300 dark:border-gray-600 text-blue-600 focus:ring-blue-500/30" checked={supportsResponses} onChange={e => setSupportsResponses(e.target.checked)} />
              <span className="text-sm text-gray-700 dark:text-gray-300">Responses 原生透传</span>
            </label>
            <label className="flex items-center gap-2 cursor-pointer">
              <input type="checkbox" className="w-4 h-4 rounded border-gray-300 dark:border-gray-600 text-blue-600 focus:ring-blue-500/30" checked={disableMiMo} onChange={e => setDisableMiMo(e.target.checked)} />
              <span className="text-sm text-gray-700 dark:text-gray-300">MiMo 已适配</span>
            </label>
          </div>

          <div className="border-t border-gray-200 dark:border-gray-800 pt-5">
            <div className="flex items-center justify-between mb-3">
              <div>
                <h3 className="text-sm font-semibold text-gray-900 dark:text-gray-100">上游 API Keys</h3>
                <p className="text-xs text-gray-400 mt-0.5">{(editorKey.keys || []).length} 个 Key 已配置</p>
              </div>
              <button onClick={() => setKeyModalOpen(true)} className="btn-secondary btn-sm">
                <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M15.75 5.25a3 3 0 013 3m3 0a6 6 0 01-7.029 5.912c-.563-.097-1.159.026-1.563.43L10.5 17.25H8.25v2.25H6v2.25H2.25v-2.818c0-.597.237-1.17.659-1.591l6.499-6.499c.404-.404.527-1 .43-1.563A6 6 0 1121.75 8.25z" /></svg>
                管理 Keys
              </button>
            </div>
          </div>

          <div className="border-t border-gray-200 dark:border-gray-800 pt-5">
            <div className="flex items-center justify-between mb-3">
              <div>
                <h3 className="text-sm font-semibold text-gray-900 dark:text-gray-100">模型列表</h3>
                <p className="text-xs text-gray-400 mt-0.5">{channelModels.length} 个模型已选择</p>
              </div>
              <button onClick={handleFetchModels} disabled={fetchingModels} className="btn-secondary btn-sm">
                <svg className={`w-4 h-4 ${fetchingModels ? 'animate-spin' : ''}`} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M3 16.5v2.25A2.25 2.25 0 005.25 21h13.5A2.25 2.25 0 0021 18.75V16.5M16.5 12L12 16.5m0 0L7.5 12m4.5 4.5V3" /></svg>
                拉取模型
              </button>
            </div>

            {channelModels.length === 0 && pendingModels.length === 0 && (
              <div className="border border-dashed border-gray-300 dark:border-gray-700 rounded-xl p-8 text-center text-sm text-gray-400">
                还没有模型，点击"拉取模型"从上游获取
              </div>
            )}

            {channelModels.length > 0 && (
              <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
                {channelModels.map(model => {
                  const isDisabled = disabledModels.includes(model)
                  const isProbing = probingModel === model
                  const probeResult = modelProbeResults[model]
                  return (
                    <div key={model} className={`px-3 py-2 rounded-lg border text-sm transition-colors ${
                      isDisabled
                        ? 'border-red-200 dark:border-red-800 bg-red-50 dark:bg-red-950/20 text-red-600 dark:text-red-400'
                        : 'border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-800/50 text-gray-800 dark:text-gray-200'
                    }`}>
                      <div className="flex items-center gap-2">
                        <span className={`flex-1 font-mono text-xs truncate ${isDisabled ? 'line-through' : ''}`}>{model}</span>
                        {probeResult && !probeResult.error && (
                          <span className={`inline-flex items-center gap-1 text-[10px] font-mono font-semibold shrink-0 ${
                            probeResult.ok ? 'text-emerald-600 dark:text-emerald-400' : 'text-red-500'
                          }`}>
                            <span className={`inline-block w-1.5 h-1.5 rounded-full ${probeResult.ok ? 'bg-emerald-500' : 'bg-red-500'}`} />
                            {probeResult.ok
                              ? `${probeResult.okCount}/${probeResult.total}`
                              : `${probeResult.okCount}成功 ${probeResult.failCount}失败`}
                          </span>
                        )}
                        {probeResult && probeResult.error && (
                          <span className="text-[10px] font-mono text-red-500 shrink-0">探活失败</span>
                        )}
                        <button
                          onClick={() => handleProbeModel(model)}
                          disabled={!isEdit || isProbing}
                          className="btn-ghost p-0.5 text-gray-400 hover:text-blue-500 disabled:opacity-30"
                          title="探活"
                        >
                          {isProbing
                            ? <span className="inline-block w-3 h-3 border-2 border-blue-500/30 border-t-blue-500 rounded-full animate-spin" />
                            : <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M21 21l-5.197-5.197m0 0A7.5 7.5 0 105.196 5.196a7.5 7.5 0 0010.607 10.607z" /></svg>
                          }
                        </button>
                        <button
                          onClick={() => toggleDisableModel(model)}
                          className={`btn-ghost p-0.5 ${isDisabled ? 'text-emerald-500 hover:text-emerald-600' : 'text-amber-500 hover:text-amber-600'}`}
                          title={isDisabled ? '启用' : '禁用'}
                        >
                          {isDisabled
                            ? <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z" /></svg>
                            : <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M18.364 18.364A9 9 0 005.636 5.636m12.728 12.728A9 9 0 015.636 5.636m12.728 12.728L5.636 5.636" /></svg>
                          }
                        </button>
                        <button
                          onClick={() => removeModel(model)}
                          className="btn-ghost p-0.5 text-gray-400 hover:text-red-500"
                          title="删除"
                        >
                          <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" /></svg>
                        </button>
                      </div>
                      {probeResult && probeResult.results && probeResult.results.length > 0 && (
                        <div className="mt-1.5 space-y-0.5">
                          {probeResult.results.map(r => (
                            <div key={r.index} className={`text-[10px] font-mono ${r.ok ? 'text-emerald-600 dark:text-emerald-400' : 'text-red-500'}`}>
                              #{r.masked_key || r.index} — {r.ok ? `${r.status} / ${r.latency_ms}ms` : `${r.status} ${r.error || 'ERR'}`}
                            </div>
                          ))}
                        </div>
                      )}
                    </div>
                  )
                })}
              </div>
            )}

          </div>

          <div className="border-t border-gray-200 dark:border-gray-800 pt-5">
            <div className="flex items-center justify-between mb-3">
              <h3 className="text-sm font-semibold text-gray-900 dark:text-gray-100">模型别名映射</h3>
              <button onClick={addMapping} className="btn-secondary btn-xs">+ 添加别名</button>
            </div>
            {modelMapping.length === 0 ? (
              <p className="text-xs text-gray-400">暂无映射配置</p>
            ) : (
              <div className="space-y-2">
                {modelMapping.map((m, i) => (
                  <div key={i} className="flex items-center gap-2">
                    <select className="select select-sm flex-1" value={m.upstream} onChange={e => updateMapping(i, 'upstream', e.target.value)}>
                      <option value="">选择上游模型</option>
                      {channelModels.map(mm => <option key={mm} value={mm}>{mm}</option>)}
                    </select>
                    <span className="text-gray-400 text-xs">→</span>
                    <input className="input input-sm flex-1 font-mono" placeholder="客户端别名" value={m.alias} onChange={e => updateMapping(i, 'alias', e.target.value)} />
                    <button onClick={() => removeMapping(i)} className="btn-ghost p-1 text-gray-400 hover:text-red-500">
                      <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" /></svg>
                    </button>
                  </div>
                ))}
              </div>
            )}
          </div>

          <div className="border-t border-gray-200 dark:border-gray-800 pt-5">
            <h3 className="text-sm font-semibold text-gray-900 dark:text-gray-100 mb-3">调度与状态</h3>
            <div className="grid grid-cols-3 gap-4">
              <div>
                <label className="block text-xs font-semibold text-gray-500 dark:text-gray-400 mb-1">优先级</label>
                <input className="input" type="number" value={priority} onChange={e => setPriority(Number(e.target.value))} min={1} />
              </div>
              <div>
                <label className="block text-xs font-semibold text-gray-500 dark:text-gray-400 mb-1">轮询权重</label>
                <input className="input" type="number" value={weight} onChange={e => setWeight(Number(e.target.value))} min={1} />
              </div>
              <div className="flex items-end pb-1">
                <label className="flex items-center gap-2 cursor-pointer">
                  <input type="checkbox" className="w-4 h-4 rounded border-gray-300 dark:border-gray-600 text-blue-600 focus:ring-blue-500/30" checked={enabled} onChange={e => setEnabled(e.target.checked)} />
                  <span className="text-sm text-gray-700 dark:text-gray-300">启用</span>
                </label>
              </div>
            </div>
          </div>
        </div>

        <div className="border-t border-gray-200 dark:border-gray-800 px-6 py-4 flex items-center justify-between">
          <span className="text-xs text-gray-400">名称、Base URL 和至少一个模型是最小可用配置</span>
          <div className="flex gap-2">
            <button onClick={onClose} className="btn-secondary">取消</button>
            <button onClick={handleSave} disabled={saving} className="btn-primary">
              {saving ? (
                <span className="inline-block w-4 h-4 border-2 border-white/30 border-t-white rounded-full animate-spin" />
              ) : (
                <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M4.5 12.75l6 6 9-13.5" /></svg>
              )}
              保存渠道
            </button>
          </div>
        </div>

        {keyModalOpen && (
          <KeyModal
            channelName={channel?.name || name.trim()}
            isPersisted={isEdit}
            initialKeys={editorKey}
            models={channelModels}
            onClose={() => setKeyModalOpen(false)}
            onSaved={handleKeySaved}
          />
        )}

        {showModelPicker && (
          <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/40" onClick={() => setShowModelPicker(false)}>
            <div className="bg-white dark:bg-gray-900 rounded-2xl shadow-xl max-w-xl w-full max-h-[80vh] flex flex-col" onClick={e => e.stopPropagation()}>
              <div className="px-6 py-4 border-b border-gray-200 dark:border-gray-800 flex items-center justify-between">
                <div>
                  <h3 className="text-base font-bold text-gray-900 dark:text-gray-100">选择模型</h3>
                  <p className="text-xs text-gray-400 mt-0.5">{pendingModels.length} 个模型从上游拉取</p>
                </div>
                <button onClick={() => setShowModelPicker(false)} className="btn-ghost p-1">
                  <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" /></svg>
                </button>
              </div>
              <div className="flex-1 overflow-y-auto scroll-thin p-4">
                <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
                  {pendingModels.map(model => (
                    <label
                      key={model}
                      className={`flex items-center gap-3 px-3 py-2.5 rounded-lg border cursor-pointer transition-colors text-sm ${
                        pickerSelection[model]
                          ? 'border-blue-400 dark:border-blue-600 bg-blue-50 dark:bg-blue-950/30 text-blue-600 dark:text-blue-300'
                          : 'border-gray-200 dark:border-gray-700 hover:border-gray-300 dark:hover:border-gray-600 text-gray-700 dark:text-gray-300'
                      }`}
                    >
                      <input
                        type="checkbox"
                        className="w-4 h-4 rounded border-gray-300 dark:border-gray-600 text-blue-600 focus:ring-blue-500/30"
                        checked={!!pickerSelection[model]}
                        onChange={() => togglePickerItem(model)}
                      />
                      <span className="font-mono text-xs truncate">{model}</span>
                    </label>
                  ))}
                </div>
              </div>
              <div className="px-6 py-4 border-t border-gray-200 dark:border-gray-800 flex items-center justify-between">
                <span className="text-xs text-gray-400">
                  已选 {Object.values(pickerSelection).filter(Boolean).length} / {pendingModels.length}
                </span>
                <div className="flex gap-2">
                  <button onClick={() => setShowModelPicker(false)} className="btn-secondary btn-sm">取消</button>
                  <button onClick={confirmModelSelection} className="btn-primary btn-sm">确认添加</button>
                </div>
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
