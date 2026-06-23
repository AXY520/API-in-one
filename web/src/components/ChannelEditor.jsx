import React, { useState, useEffect, useCallback, useMemo } from 'react'
import {
  CheckCircle2,
  ChevronRight,
  CircleAlert,
  Plus,
  RotateCcw,
  Save,
  Search,
  Server,
  SlidersHorizontal,
  Trash2,
  X,
} from 'lucide-react'
import { toast } from './Toast'
import { createChannel, updateChannel, deleteChannel, fetchChannelKeys, fetchModels, probeChannelKeys, setChannelEnabled } from '../api'

function channelTotals(channel) {
  const stats = channel?.key_stats || []
  return {
    requests: stats.reduce((sum, item) => sum + (item.total_requests || 0), 0),
    success: stats.reduce((sum, item) => sum + (item.success_requests || 0), 0),
    failure: stats.reduce((sum, item) => sum + (item.failure_requests || 0), 0),
    latency: stats.length ? Math.round(stats.reduce((sum, item) => sum + (item.last_latency_ms || 0), 0) / stats.length) : 0,
  }
}

function statusMeta(channel) {
  if (!channel) return { label: '新建', cls: 'bg-blue-100 text-blue-600 dark:bg-blue-950 dark:text-blue-300' }
  if (channel.enabled === false) return { label: '停用', cls: 'bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-300' }
  if (channel.healthy) return { label: '健康', cls: 'bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300' }
  return { label: '异常', cls: 'bg-rose-100 text-rose-700 dark:bg-rose-950 dark:text-rose-300' }
}

function Field({ label, children, className = '' }) {
  return (
    <label className={`block ${className}`}>
      <span className="mb-1 block text-xs font-semibold text-slate-500 dark:text-slate-400">{label}</span>
      {children}
    </label>
  )
}

function maskKeyLocal(key) {
  if (!key || key.length <= 8) return '****'
  return key.slice(0, 4) + '****' + key.slice(-4)
}

function showURLWarning(url) {
  if (!url) return false
  const path = url.toLowerCase().trim()
  return path.includes('/chat/completions') || path.includes('/messages') || path.includes('/generatecontent') || path.includes('/v1/messages')
}

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

function ChannelListPanel({ channels, selectedName, query, onQuery, onSelect, onAddNew, onToggleEnabled }) {
  return (
    <aside className="card flex min-h-[18rem] flex-col overflow-hidden lg:h-[calc(100vh-9rem)]">
      <div className="border-b p-4">
        <div className="flex items-center justify-between gap-3">
          <div>
            <h2 className="section-title">渠道</h2>
            <p className="section-subtitle mt-1">{channels.length} 个上游配置</p>
          </div>
          <button onClick={onAddNew} className="btn-primary px-3 py-2">
            <Plus className="h-4 w-4" />
            <span className="hidden xl:inline">新建</span>
          </button>
        </div>
        <div className="relative mt-3">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-400" />
          <input
            className="input input-sm pl-9"
            value={query}
            onChange={e => onQuery(e.target.value)}
            placeholder="搜索渠道、类型、URL"
          />
        </div>
      </div>
      <div className="flex-1 overflow-y-auto p-2 scroll-thin">
        {channels.length === 0 ? (
          <div className="rounded-lg border border-dashed p-6 text-center text-sm text-slate-400">没有匹配的渠道</div>
        ) : channels.map(channel => {
          const active = channel.name === selectedName
          const enabled = channel.enabled !== false
          return (
            <div
              key={channel.name}
              className={`mb-2 flex items-center gap-2 rounded-lg border p-2 transition-colors ${
                active
                  ? 'border-blue-500 bg-blue-50/70 dark:bg-blue-950/30'
                  : 'bg-white hover:border-slate-300 hover:bg-slate-50 dark:bg-slate-900 dark:hover:bg-slate-800/70'
              }`}
            >
              <button
                onClick={() => onSelect(channel.name)}
                className="flex min-w-0 flex-1 items-center justify-between gap-3 rounded-md px-1 py-1 text-left"
              >
                <span className={`min-w-0 truncate text-sm font-medium ${enabled ? 'text-slate-950 dark:text-slate-50' : 'text-slate-400 dark:text-slate-500'}`}>
                  {channel.name}
                </span>
                <ChevronRight className={`h-4 w-4 shrink-0 ${active ? 'text-blue-600 dark:text-blue-300' : 'text-slate-400'}`} />
              </button>
              <button
                onClick={() => onToggleEnabled(channel)}
                className={`shrink-0 rounded-md px-2 py-1 text-[11px] font-medium transition-colors ${
                  enabled
                    ? 'bg-emerald-100 text-emerald-700 hover:bg-emerald-200 dark:bg-emerald-950 dark:text-emerald-300'
                    : 'bg-slate-100 text-slate-500 hover:bg-slate-200 dark:bg-slate-800 dark:text-slate-300'
                }`}
              >
                {enabled ? '启用' : '停用'}
              </button>
            </div>
          )
        })}
      </div>
    </aside>
  )
}

export default function ChannelEditor({ channels: propChannels, refreshChannels }) {
  const [selectedName, setSelectedName] = useState(null)
  const [addingNew, setAddingNew] = useState(false)
  const [saving, setSaving] = useState(false)
  const [query, setQuery] = useState('')

  const [name, setName] = useState('')
  const [type, setType] = useState('openai')
  const [baseURL, setBaseURL] = useState('')
  const [baseURLClaude, setBaseURLClaude] = useState('')
  const [baseURLGemini, setBaseURLGemini] = useState('')
  const [supportsResponses, setSupportsResponses] = useState(false)
  const [responsesOnly, setResponsesOnly] = useState(false)
  const [disableMiMo, setDisableMiMo] = useState(false)
  const [channelModels, setChannelModels] = useState([])
  const [disabledModels, setDisabledModels] = useState([])
  const [priority, setPriority] = useState(10)
  const [weight, setWeight] = useState(100)
  const [enabled, setEnabled] = useState(true)
  const [editorKey, setEditorKey] = useState({})
  const [probeModel, setProbeModel] = useState('')
  const [probingKeys, setProbingKeys] = useState(false)
  const [modelMapping, setModelMapping] = useState([])
  const [fetchingModels, setFetchingModels] = useState(false)
  const [probingModel, setProbingModel] = useState(null)
  const [modelProbeResults, setModelProbeResults] = useState({})
  const [probingProtocols, setProbingProtocols] = useState({})
  const [protocolProbeResults, setProtocolProbeResults] = useState({})
  const [showModelPicker, setShowModelPicker] = useState(false)
  const [pendingModels, setPendingModels] = useState([])
  const [pickerSelection, setPickerSelection] = useState({})
  const [showBulkAdd, setShowBulkAdd] = useState(false)
  const [bulkText, setBulkText] = useState('')

  const channel = propChannels.find(c => c.name === selectedName) || null
  const isNew = addingNew && !selectedName

  const resetForm = useCallback(() => {
    setName('')
    setType('openai')
    setBaseURL('')
    setBaseURLClaude('')
    setBaseURLGemini('')
    setSupportsResponses(false)
    setResponsesOnly(false)
    setDisableMiMo(false)
    setChannelModels([])
    setDisabledModels([])
    setPriority(10)
    setWeight(100)
    setEnabled(true)
    setEditorKey({})
    setProbeModel('')
    setProbingKeys(false)
    setModelMapping([])
    setModelProbeResults({})
    setProbingProtocols({})
    setProtocolProbeResults({})
    setFetchingModels(false)
    setProbingModel(null)
  }, [])

  useEffect(() => {
    if (channel) {
      setName(channel.name)
      setType(channel.type)
      setBaseURL(channel.base_url)
      setBaseURLClaude(channel.base_url_claude || '')
      setBaseURLGemini(channel.base_url_gemini || '')
      setSupportsResponses(channel.supports_responses || false)
      setResponsesOnly(channel.responses_only || false)
      setDisableMiMo(channel.disable_mimo_compat || false)
      setChannelModels(channel.models || [])
      setDisabledModels(channel.disabled_models || [])
      setPriority(channel.priority)
      setWeight(channel.weight)
      setEnabled(channel.enabled !== false)
      setModelMapping(Object.entries(channel.model_mapping || {}).map(([alias, upstream]) => ({ alias, upstream })))
      setModelProbeResults({})
      setProbingProtocols({})
      setProtocolProbeResults({})
      setAddingNew(false)
    } else if (!addingNew) {
      resetForm()
    }
  }, [channel, addingNew, resetForm])

  useEffect(() => {
    if (channel?.name) {
      fetchChannelKeys(channel.name).then(data => {
        const keys = data.keys || []
        const rawKeyModels = data.key_models || {}
        const normalizedKeyModels = {}
        keys.forEach((key, index) => {
          const models = rawKeyModels[key] || rawKeyModels[String(index)] || []
          if (models.length > 0) normalizedKeyModels[key] = models
        })
        setEditorKey({
          keys,
          disabledKeys: data.disabled_keys || [],
          keyModels: normalizedKeyModels,
          keyStats: channel?.key_stats || [],
        })
        if (!probeModel && keys.length > 0 && channel?.models?.length > 0) {
          setProbeModel(channel.models[0])
        }
      }).catch(() => {})
    }
  }, [channel?.name, channel?.models, probeModel])

  const filteredChannels = useMemo(() => {
    const q = query.trim().toLowerCase()
    const list = propChannels.filter(c => !c.temporary)
    if (!q) return list
    return list.filter(c => [
      c.name,
      c.type,
      c.base_url,
      c.base_url_claude,
      c.base_url_gemini,
    ].filter(Boolean).some(value => String(value).toLowerCase().includes(q)))
  }, [propChannels, query])

  const handleSelect = (chName) => {
    setAddingNew(false)
    setSelectedName(chName)
  }

  const handleAddNew = () => {
    setSelectedName(null)
    setAddingNew(true)
    resetForm()
  }

  const handleToggleChannelEnabled = async (target) => {
    const next = target.enabled === false
    try {
      await setChannelEnabled(target.name, next)
      toast(`${target.name} 已${next ? '启用' : '停用'}`)
      await refreshChannels()
      if (target.name === selectedName) {
        setEnabled(next)
      }
    } catch (e) {
      toast('切换渠道状态失败: ' + e.message, true)
    }
  }

  const removeModel = (model) => {
    setChannelModels(prev => prev.filter(m => m !== model))
    setDisabledModels(prev => prev.filter(m => m !== model))
    setModelMapping(prev => prev.filter(({ alias, upstream }) => alias !== model && upstream !== model))
  }

  const toggleDisableModel = (model) => {
    setDisabledModels(prev => prev.includes(model) ? prev.filter(m => m !== model) : [...prev, model])
  }

  const handleProbeModel = async (model) => {
    const chName = channel?.name || name
    if (!chName || !channel) {
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

  const handleProbeProtocol = async (proto) => {
    if (!channel) {
      toast('请先保存该渠道后再进行协议探测', true)
      return
    }
    const testModel = channelModels[0] || (channel.models && channel.models[0]) || ''
    if (!testModel) {
      toast('探测协议需要该渠道配置至少一个有效模型', true)
      return
    }

    setProbingProtocols(prev => ({ ...prev, [proto]: true }))
    try {
      const result = await probeChannelKeys(channel.name, { model: testModel, protocol: proto })
      const firstResult = result.results?.[0]
      if (firstResult) {
        setProtocolProbeResults(prev => ({
          ...prev,
          [proto]: {
            ok: firstResult.ok,
            status: firstResult.status,
            latency: firstResult.latency_ms,
            error: firstResult.error
          }
        }))
        if (firstResult.ok) {
          toast(`协议探测 [${proto}] 成功! 延迟 ${firstResult.latency_ms}ms`)
        } else {
          toast(`协议探测 [${proto}] 失败: ${firstResult.error || '未知错误'}`, true)
        }
      } else {
        toast('没有可用于探测的密钥', true)
      }
    } catch (e) {
      setProtocolProbeResults(prev => ({
        ...prev,
        [proto]: {
          ok: false,
          error: e.message
        }
      }))
      toast(`探测错误: ${e.message}`, true)
    } finally {
      setProbingProtocols(prev => ({ ...prev, [proto]: false }))
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
        supports_responses: supportsResponses || responsesOnly,
        responses_only: responsesOnly,
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
      if (channel) {
        await updateChannel(channel.name, body)
        toast('渠道已更新')
      } else {
        await createChannel(body)
        toast('渠道已创建')
      }
      await refreshChannels()
      if (!channel) {
        setAddingNew(false)
        setSelectedName(name.trim())
      }
    } catch (e) {
      toast('保存失败: ' + e.message, true)
    } finally {
      setSaving(false)
    }
  }

  const handleDelete = async () => {
    const chName = channel?.name
    if (!chName) return
    if (!window.confirm(`确认永久删除渠道 "${chName}" 吗？`)) return
    try {
      await deleteChannel(chName)
      toast(`渠道 ${chName} 已删除`)
      setSelectedName(null)
      setAddingNew(false)
      resetForm()
      await refreshChannels()
    } catch (e) {
      toast('删除失败: ' + e.message, true)
    }
  }

  const handleFetchModels = async () => {
    const fetchBaseURL = routeBaseURL(type, baseURL, baseURLClaude, baseURLGemini)
    if (!fetchBaseURL) {
      toast('请先填写当前类型的 Base URL', true)
      return
    }
    let key = editorKey.keys?.[0] || ''
    if (!key && channel) {
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

  const addKeyRow = () => {
    setEditorKey(prev => ({
      ...prev,
      keys: [...(prev.keys || []), ''],
    }))
  }

  const handleBulkAddKeys = () => {
    if (!bulkText.trim()) {
      setShowBulkAdd(false)
      return
    }
    const items = bulkText.split(/[\n, ]+/).map(x => x.trim()).filter(x => x.length > 0)
    if (items.length === 0) {
      toast('未识别到有效的 Keys', true)
      return
    }
    setEditorKey(prev => ({
      ...prev,
      keys: [...(prev.keys || []), ...items],
    }))
    setBulkText('')
    setShowBulkAdd(false)
    toast(`已成功导入 ${items.length} 个 Keys`)
  }

  const updateKeyRow = (index, value) => {
    setEditorKey(prev => ({
      ...prev,
      keys: (prev.keys || []).map((key, idx) => idx === index ? value : key),
    }))
  }

  const removeKeyRow = (index) => {
    setEditorKey(prev => {
      const oldKeys = prev.keys || []
      const removedKey = oldKeys[index]
      const nextKeys = oldKeys.filter((_, idx) => idx !== index)
      const nextDisabled = (prev.disabledKeys || []).filter(key => key !== removedKey)
      return {
        ...prev,
        keys: nextKeys,
        disabledKeys: nextDisabled,
      }
    })
  }

  const toggleKeyDisabled = (index) => {
    setEditorKey(prev => {
      const key = (prev.keys || [])[index]
      if (!key) return prev
      const disabledKeys = prev.disabledKeys || []
      return {
        ...prev,
        disabledKeys: disabledKeys.includes(key)
          ? disabledKeys.filter(item => item !== key)
          : [...disabledKeys, key],
      }
    })
  }

  const handleProbeKeys = async (index = null) => {
    if (!channel?.name) {
      toast('请先保存渠道后再探活', true)
      return
    }
    if (!probeModel && (editorKey.keys || []).length > 0) {
      toast('请先选择探活模型', true)
      return
    }
    setProbingKeys(true)
    try {
      const result = await probeChannelKeys(channel.name, { model: probeModel, index, protocol: probeProtocolForChannel(channel) })
      const results = result.results || []
      setEditorKey(prev => {
        const keyStats = [...(prev.keyStats || [])]
        results.forEach(item => {
          keyStats[item.index] = {
            ...(keyStats[item.index] || {}),
            last_latency_ms: item.latency_ms || 0,
            healthy: !!item.ok,
            probe: item,
          }
        })
        return { ...prev, keyStats }
      })
      toast(`探活完成: ${result.model || probeModel}`)
    } catch (e) {
      toast('探活失败: ' + e.message, true)
    } finally {
      setProbingKeys(false)
    }
  }

  const addMapping = () => setModelMapping(prev => [...prev, { alias: '', upstream: '' }])
  const updateMapping = (i, field, value) => {
    setModelMapping(prev => prev.map((m, idx) => idx === i ? { ...m, [field]: value } : m))
  }
  const removeMapping = (i) => setModelMapping(prev => prev.filter((_, idx) => idx !== i))

  const hasSelection = channel || isNew
  const totals = channelTotals(channel)
  const meta = statusMeta(channel)
  const keyCount = editorKey.keys?.length || 0
  const keyStats = editorKey.keyStats || []

  return (
    <div className="grid gap-4 lg:grid-cols-[22rem_minmax(0,1fr)]">
      <ChannelListPanel
        channels={filteredChannels}
        selectedName={selectedName}
        query={query}
        onQuery={setQuery}
        onSelect={handleSelect}
        onAddNew={handleAddNew}
        onToggleEnabled={handleToggleChannelEnabled}
      />

      <section className="min-w-0">
        {!hasSelection ? (
          <div className="card flex min-h-[32rem] items-center justify-center p-8 text-center">
            <div>
              <div className="mx-auto flex h-12 w-12 items-center justify-center rounded-lg bg-blue-50 text-blue-600 dark:bg-blue-950 dark:text-blue-300">
                <Server className="h-6 w-6" />
              </div>
              <h2 className="mt-4 text-lg font-semibold text-slate-950 dark:text-slate-50">选择渠道</h2>
            </div>
          </div>
        ) : (
          <div className="space-y-4">
            <div className="card p-5">
              <div className="flex flex-col gap-4 xl:flex-row xl:items-start xl:justify-between">
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className={`badge ${meta.cls}`}>{meta.label}</span>
                    <span className="chip bg-slate-50 text-slate-600 dark:bg-slate-950 dark:text-slate-300">{type}</span>
                    {supportsResponses && <span className="badge bg-blue-100 text-blue-600 dark:bg-blue-950 dark:text-blue-300">Responses</span>}
                    {responsesOnly && <span className="badge bg-violet-100 text-violet-700 dark:bg-violet-950 dark:text-violet-300">仅 Responses</span>}
                  </div>
                  <h2 className="mt-3 truncate text-2xl font-semibold text-slate-950 dark:text-slate-50">
                    {isNew ? '新建渠道' : channel.name}
                  </h2>
                  <p className="mt-1 truncate font-mono text-xs text-slate-500 dark:text-slate-400">{baseURL || '等待填写 Base URL'}</p>
                </div>
                <div className="flex flex-wrap gap-2">
                  {channel && (
                    <button onClick={handleDelete} className="btn-secondary text-rose-600 dark:text-rose-400">
                      <Trash2 className="h-4 w-4" />
                      删除
                    </button>
                  )}
                  <button onClick={handleSave} disabled={saving} className="btn-primary">
                    {saving ? <RotateCcw className="h-4 w-4 animate-spin" /> : <Save className="h-4 w-4" />}
                    保存渠道
                  </button>
                </div>
              </div>

              <div className="mt-5 grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
                {[
                  ['请求量', totals.requests.toLocaleString()],
                  ['成功/失败', `${totals.success.toLocaleString()} / ${totals.failure.toLocaleString()}`],
                  ['平均延迟', `${totals.latency.toLocaleString()} ms`],
                  ['Key / 模型', `${keyCount} / ${channelModels.length}`],
                ].map(([label, value]) => (
                  <div key={label} className="rounded-lg border bg-slate-50/70 p-3 dark:bg-slate-950/50">
                    <div className="stat-label">{label}</div>
                    <div className="mt-1 font-mono text-lg font-semibold text-slate-950 dark:text-slate-50">{value}</div>
                  </div>
                ))}
              </div>
            </div>

            <div className="grid gap-4 xl:grid-cols-[1fr_21rem]">
              <div className="space-y-4 min-w-0">
                <div className="card p-5">
                  <div className="mb-4 flex items-center gap-2 border-b pb-3">
                    <Server className="h-4 w-4 text-blue-600 dark:text-blue-300" />
                    <h3 className="section-title">基础路由</h3>
                  </div>
                  <div className="grid gap-4 md:grid-cols-2">
                    <Field label="渠道名称 *">
                      <input className="input" value={name} onChange={e => setName(e.target.value)} placeholder="azure-openai-west" disabled={!!channel} />
                    </Field>
                    <Field label="渠道类型">
                      <select className="select" value={type} onChange={e => setType(e.target.value)}>
                        <option value="openai">OpenAI</option>
                        <option value="claude">Claude</option>
                        <option value="gemini">Gemini</option>
                      </select>
                    </Field>
                    <Field label="标准 Base URL" className="md:col-span-2">
                      <input className="input font-mono" value={baseURL} onChange={e => setBaseURL(e.target.value)} placeholder="https://api.openai.com/v1" />
                      {showURLWarning(baseURL) && (
                        <span className="mt-1 block text-[11px] text-amber-600 dark:text-amber-400 font-medium animate-fade-in">
                          ⚠️ 注意：Base URL 应为基础根路径，请勿包含 `/chat/completions` 等特定终点。
                        </span>
                      )}
                    </Field>
                    <Field label="Claude 专有 URL">
                      <input className="input font-mono" value={baseURLClaude} onChange={e => setBaseURLClaude(e.target.value)} placeholder="可选" />
                      {showURLWarning(baseURLClaude) && (
                        <span className="mt-1 block text-[11px] text-amber-600 dark:text-amber-400 font-medium animate-fade-in">
                          ⚠️ 注意：URL 应为根路径，请勿包含 `/messages` 等特定终点。
                        </span>
                      )}
                    </Field>
                    <Field label="Gemini 专有 URL">
                      <input className="input font-mono" value={baseURLGemini} onChange={e => setBaseURLGemini(e.target.value)} placeholder="可选" />
                      {showURLWarning(baseURLGemini) && (
                        <span className="mt-1 block text-[11px] text-amber-600 dark:text-amber-400 font-medium animate-fade-in">
                          ⚠️ 注意：URL 应为根路径，请勿包含 `/generateContent` 等特定终点。
                        </span>
                      )}
                    </Field>
                  </div>
                  <div className="mt-6 border-t pt-4">
                    <h4 className="text-xs font-semibold uppercase tracking-wider text-slate-400 dark:text-slate-500 mb-3">支持的协议能力 (Protocol Capabilities)</h4>
                    <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
                      
                      {/* OpenAI Chat */}
                      <label className="flex cursor-pointer items-start gap-3 rounded-lg border p-3 bg-white dark:bg-slate-900/50 hover:border-blue-500 hover:shadow-sm transition-all duration-200">
                        <input
                          type="checkbox"
                          className="mt-1 h-4 w-4 rounded border-slate-300 text-blue-600 focus:ring-blue-600/20"
                          checked={type === "openai"}
                          onChange={() => {
                            if (type !== "openai") {
                              setType("openai");
                            }
                          }}
                        />
                        <div className="flex-1 min-w-0">
                          <div className="flex items-center justify-between gap-2 text-sm font-medium text-slate-900 dark:text-slate-100">
                            <span>OpenAI Chat</span>
                            {channel && (
                              <button
                                type="button"
                                onClick={(e) => { e.preventDefault(); e.stopPropagation(); handleProbeProtocol('openai'); }}
                                disabled={probingProtocols['openai']}
                                className="text-[10px] text-blue-500 hover:text-blue-600 hover:underline cursor-pointer"
                              >
                                {probingProtocols['openai'] ? '探测中...' : '探测 ⚡'}
                              </button>
                            )}
                          </div>
                          <div className="mt-1 text-[11px] text-slate-500 dark:text-slate-400 leading-normal">默认的 Chat 完成接口 /v1/chat/completions</div>
                          {protocolProbeResults['openai'] && (
                            <div className={`mt-2 text-[10px] font-mono font-semibold ${
                              protocolProbeResults['openai'].ok ? 'text-emerald-600 dark:text-emerald-400' : 'text-rose-600 dark:text-rose-400'
                            }`}>
                              {protocolProbeResults['openai'].ok 
                                ? `✓ 支持 (延迟 ${protocolProbeResults['openai'].latency}ms)` 
                                : `✗ 不支持 (${protocolProbeResults['openai'].status || '错误'}: ${protocolProbeResults['openai'].error || '未知'})`
                              }
                            </div>
                          )}
                        </div>
                      </label>

                      {/* Claude Messages */}
                      <label className="flex cursor-pointer items-start gap-3 rounded-lg border p-3 bg-white dark:bg-slate-900/50 hover:border-indigo-500 hover:shadow-sm transition-all duration-200">
                        <input
                          type="checkbox"
                          className="mt-1 h-4 w-4 rounded border-slate-300 text-blue-600 focus:ring-blue-600/20"
                          checked={type === "claude" || baseURLClaude !== ""}
                          onChange={(e) => {
                            if (e.target.checked) {
                              if (type !== "claude" && baseURLClaude === "") {
                                setBaseURLClaude("https://api.anthropic.com");
                              }
                            } else {
                              if (type === "claude") {
                                setType("openai");
                              }
                              setBaseURLClaude("");
                            }
                          }}
                        />
                        <div className="flex-1 min-w-0">
                          <div className="flex items-center justify-between gap-2 text-sm font-medium text-slate-900 dark:text-slate-100">
                            <span>Claude Messages</span>
                            {channel && (
                              <button
                                type="button"
                                onClick={(e) => { e.preventDefault(); e.stopPropagation(); handleProbeProtocol('claude'); }}
                                disabled={probingProtocols['claude']}
                                className="text-[10px] text-indigo-500 hover:text-indigo-600 hover:underline cursor-pointer"
                              >
                                {probingProtocols['claude'] ? '探测中...' : '探测 ⚡'}
                              </button>
                            )}
                          </div>
                          <div className="mt-1 text-[11px] text-slate-500 dark:text-slate-400 leading-normal">Anthropic Claude Messages 原生接口协议</div>
                          {protocolProbeResults['claude'] && (
                            <div className={`mt-2 text-[10px] font-mono font-semibold ${
                              protocolProbeResults['claude'].ok ? 'text-emerald-600 dark:text-emerald-400' : 'text-rose-600 dark:text-rose-400'
                            }`}>
                              {protocolProbeResults['claude'].ok 
                                ? `✓ 支持 (延迟 ${protocolProbeResults['claude'].latency}ms)` 
                                : `✗ 不支持 (${protocolProbeResults['claude'].status || '错误'}: ${protocolProbeResults['claude'].error || '未知'})`
                              }
                            </div>
                          )}
                        </div>
                      </label>

                      {/* Gemini Generate */}
                      <label className="flex cursor-pointer items-start gap-3 rounded-lg border p-3 bg-white dark:bg-slate-900/50 hover:border-teal-500 hover:shadow-sm transition-all duration-200">
                        <input
                          type="checkbox"
                          className="mt-1 h-4 w-4 rounded border-slate-300 text-blue-600 focus:ring-blue-600/20"
                          checked={type === "gemini" || baseURLGemini !== ""}
                          onChange={(e) => {
                            if (e.target.checked) {
                              if (type !== "gemini" && baseURLGemini === "") {
                                setBaseURLGemini("https://generativelanguage.googleapis.com");
                              }
                            } else {
                              if (type === "gemini") {
                                setType("openai");
                              }
                              setBaseURLGemini("");
                            }
                          }}
                        />
                        <div className="flex-1 min-w-0">
                          <div className="flex items-center justify-between gap-2 text-sm font-medium text-slate-900 dark:text-slate-100">
                            <span>Gemini Generate</span>
                            {channel && (
                              <button
                                type="button"
                                onClick={(e) => { e.preventDefault(); e.stopPropagation(); handleProbeProtocol('gemini'); }}
                                disabled={probingProtocols['gemini']}
                                className="text-[10px] text-teal-500 hover:text-teal-600 hover:underline cursor-pointer"
                              >
                                {probingProtocols['gemini'] ? '探测中...' : '探测 ⚡'}
                              </button>
                            )}
                          </div>
                          <div className="mt-1 text-[11px] text-slate-500 dark:text-slate-400 leading-normal">Google Gemini GenerateContent 原生接口协议</div>
                          {protocolProbeResults['gemini'] && (
                            <div className={`mt-2 text-[10px] font-mono font-semibold ${
                              protocolProbeResults['gemini'].ok ? 'text-emerald-600 dark:text-emerald-400' : 'text-rose-600 dark:text-rose-400'
                            }`}>
                              {protocolProbeResults['gemini'].ok 
                                ? `✓ 支持 (延迟 ${protocolProbeResults['gemini'].latency}ms)` 
                                : `✗ 不支持 (${protocolProbeResults['gemini'].status || '错误'}: ${protocolProbeResults['gemini'].error || '未知'})`
                              }
                            </div>
                          )}
                        </div>
                      </label>

                      {/* Responses API */}
                      <label className="flex cursor-pointer items-start gap-3 rounded-lg border p-3 bg-white dark:bg-slate-900/50 hover:border-purple-500 hover:shadow-sm transition-all duration-200">
                        <input
                          type="checkbox"
                          className="mt-1 h-4 w-4 rounded border-slate-300 text-blue-600 focus:ring-blue-600/20"
                          checked={supportsResponses || responsesOnly}
                          onChange={(e) => {
                            setSupportsResponses(e.target.checked);
                            if (!e.target.checked) setResponsesOnly(false);
                          }}
                        />
                        <div className="flex-1 min-w-0">
                          <div className="flex items-center justify-between gap-2 text-sm font-medium text-slate-900 dark:text-slate-100">
                            <span>Responses API</span>
                            {channel && (
                              <button
                                type="button"
                                onClick={(e) => { e.preventDefault(); e.stopPropagation(); handleProbeProtocol('responses'); }}
                                disabled={probingProtocols['responses']}
                                className="text-[10px] text-purple-500 hover:text-purple-600 hover:underline cursor-pointer"
                              >
                                {probingProtocols['responses'] ? '探测中...' : '探测 ⚡'}
                              </button>
                            )}
                          </div>
                          <div className="mt-1 text-[11px] text-slate-500 dark:text-slate-400 leading-normal">非 Chat 格式的纯回复列表协议透传</div>
                          {protocolProbeResults['responses'] && (
                            <div className={`mt-2 text-[10px] font-mono font-semibold ${
                              protocolProbeResults['responses'].ok ? 'text-emerald-600 dark:text-emerald-400' : 'text-rose-600 dark:text-rose-400'
                            }`}>
                              {protocolProbeResults['responses'].ok 
                                ? `✓ 支持 (延迟 ${protocolProbeResults['responses'].latency}ms)` 
                                : `✗ 不支持 (${protocolProbeResults['responses'].status || '错误'}: ${protocolProbeResults['responses'].error || '未知'})`
                              }
                            </div>
                          )}
                        </div>
                      </label>

                      {/* Upstream Responses Only */}
                      <label className="flex cursor-pointer items-start gap-3 rounded-lg border p-3 bg-white dark:bg-slate-900/50 hover:border-violet-500 hover:shadow-sm transition-all duration-200">
                        <input
                          type="checkbox"
                          className="mt-1 h-4 w-4 rounded border-slate-300 text-blue-600 focus:ring-blue-600/20"
                          checked={responsesOnly}
                          onChange={(e) => {
                            setResponsesOnly(e.target.checked);
                            if (e.target.checked) setSupportsResponses(true);
                          }}
                        />
                        <div className="flex-1 min-w-0">
                          <div className="text-sm font-medium text-slate-900 dark:text-slate-100">上游仅 Responses</div>
                          <div className="mt-1 text-[11px] text-slate-500 dark:text-slate-400 leading-normal">Chat 入站也强制转换为 Responses 向上游请求</div>
                        </div>
                      </label>

                      {/* Mimo XML Compatibility */}
                      <label className="flex cursor-pointer items-start gap-3 rounded-lg border p-3 bg-white dark:bg-slate-900/50 hover:border-amber-500 hover:shadow-sm transition-all duration-200">
                        <input
                          type="checkbox"
                          className="mt-1 h-4 w-4 rounded border-slate-300 text-blue-600 focus:ring-blue-600/20"
                          checked={!disableMiMo}
                          onChange={(e) => {
                            setDisableMiMo(!e.target.checked);
                          }}
                        />
                        <div className="flex-1 min-w-0">
                          <div className="text-sm font-medium text-slate-900 dark:text-slate-100">Mimo XML 兼容</div>
                          <div className="mt-1 text-[11px] text-slate-500 dark:text-slate-400 leading-normal">解析小米 mimo XML 伪工具调用并转换为标准 JSON</div>
                        </div>
                      </label>

                    </div>
                  </div>
                </div>

                <div className="card p-5">
                  <div className="mb-4 flex flex-wrap items-center justify-between gap-3 border-b pb-3">
                    <div className="flex items-center gap-2">
                      <SlidersHorizontal className="h-4 w-4 text-blue-600 dark:text-blue-300" />
                      <div>
                        <h3 className="section-title">模型与映射</h3>
                      </div>
                    </div>
                    <button onClick={handleFetchModels} disabled={fetchingModels} className="btn-secondary">
                      {fetchingModels ? <RotateCcw className="h-4 w-4 animate-spin" /> : <Search className="h-4 w-4" />}
                      拉取模型
                    </button>
                  </div>

                  {channelModels.length === 0 ? (
                    <div className="rounded-lg border border-dashed p-8 text-center text-sm text-slate-400">
                      还没有模型。先配置 Key，再从上游拉取模型。
                    </div>
                  ) : (
                    <div className="grid gap-2 md:grid-cols-2">
                      {channelModels.map(model => {
                        const isDisabled = disabledModels.includes(model)
                        const isProbing = probingModel === model
                        const probeResult = modelProbeResults[model]
                        return (
                          <div key={model} className={`rounded-lg border p-3 ${isDisabled ? 'border-rose-200 bg-rose-50/70 dark:border-rose-900 dark:bg-rose-950/20' : 'bg-slate-50/70 dark:bg-slate-950/50'}`}>
                            <div className="flex items-center gap-2">
                              <span className={`min-w-0 flex-1 truncate font-mono text-xs ${isDisabled ? 'text-rose-700 line-through dark:text-rose-300' : 'text-slate-800 dark:text-slate-200'}`}>{model}</span>
                              {probeResult && (
                                <span className={`badge ${probeResult.ok ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300' : 'bg-rose-100 text-rose-700 dark:bg-rose-950 dark:text-rose-300'}`}>
                                  {probeResult.error ? 'ERR' : `${probeResult.okCount}/${probeResult.total}`}
                                </span>
                              )}
                            </div>
                            <div className="mt-3 flex flex-wrap gap-2">
                              <button onClick={() => handleProbeModel(model)} disabled={isProbing} className="btn-secondary btn-xs">
                                {isProbing ? <RotateCcw className="h-3.5 w-3.5 animate-spin" /> : <Search className="h-3.5 w-3.5" />}
                                探活
                              </button>
                              <button onClick={() => toggleDisableModel(model)} className="btn-secondary btn-xs">
                                {isDisabled ? <CheckCircle2 className="h-3.5 w-3.5" /> : <CircleAlert className="h-3.5 w-3.5" />}
                                {isDisabled ? '启用' : '禁用'}
                              </button>
                              <button onClick={() => removeModel(model)} className="btn-secondary btn-xs text-rose-600 dark:text-rose-400">
                                <X className="h-3.5 w-3.5" />
                                移除
                              </button>
                            </div>
                          </div>
                        )
                      })}
                    </div>
                  )}

                  <div className="mt-5 border-t pt-4">
                    <div className="mb-3 flex items-center justify-between">
                      <div>
                        <h4 className="text-sm font-semibold text-slate-900 dark:text-slate-100">别名映射</h4>
                        <p className="section-subtitle mt-1">左边是上游模型，右边是客户端看到的模型名</p>
                      </div>
                      <button onClick={addMapping} className="btn-secondary btn-xs">
                        <Plus className="h-3.5 w-3.5" />
                        添加
                      </button>
                    </div>
                    {modelMapping.length === 0 ? (
                      <div className="rounded-lg border border-dashed p-4 text-sm text-slate-400">暂无映射</div>
                    ) : (
                      <div className="space-y-2">
                        {modelMapping.map((m, i) => (
                          <div key={i} className="grid gap-2 md:grid-cols-[1fr_auto_1fr_auto] md:items-center">
                            <select className="select select-sm" value={m.upstream} onChange={e => updateMapping(i, 'upstream', e.target.value)}>
                              <option value="">选择上游模型</option>
                              {channelModels.map(mm => <option key={mm} value={mm}>{mm}</option>)}
                            </select>
                            <span className="hidden text-slate-400 md:block">→</span>
                            <input className="input input-sm font-mono" placeholder="客户端别名" value={m.alias} onChange={e => updateMapping(i, 'alias', e.target.value)} />
                            <button onClick={() => removeMapping(i)} className="btn-secondary btn-sm text-rose-600 dark:text-rose-400">
                              <Trash2 className="h-4 w-4" />
                            </button>
                          </div>
                        ))}
                      </div>
                    )}
                  </div>
                </div>
              </div>

              <aside className="space-y-4">
                <div className="card p-5">
                  <div className="flex items-center justify-between gap-3">
                    <h3 className="section-title">上游 Keys</h3>
                    <span className="text-xs font-mono text-slate-500 dark:text-slate-400">{keyCount}</span>
                  </div>
                  <div className="mt-4 flex flex-wrap items-center gap-2">
                    <select className="select select-sm min-w-0 flex-1 font-mono" value={probeModel} onChange={e => setProbeModel(e.target.value)}>
                      {channelModels.length === 0 && <option value="">选择模型</option>}
                      {channelModels.map(model => <option key={model} value={model}>{model}</option>)}
                    </select>
                    <button onClick={() => handleProbeKeys()} disabled={probingKeys || !channel} className="btn-secondary btn-sm">
                      {probingKeys ? <RotateCcw className="h-4 w-4 animate-spin" /> : <Search className="h-4 w-4" />}
                      探活
                    </button>
                    <button onClick={() => setShowBulkAdd(true)} className="btn-secondary btn-sm font-medium">
                      批量导入
                    </button>
                    <button onClick={addKeyRow} className="btn-secondary btn-sm">
                      <Plus className="h-4 w-4" />
                    </button>
                  </div>

                  <div className="mt-4 space-y-3">
                    {keyCount === 0 ? (
                      <div className="rounded-lg border border-dashed p-4 text-center text-sm text-slate-400">暂无 Key</div>
                    ) : (
                      (editorKey.keys || []).map((key, index) => {
                        const stat = keyStats[index] || {}
                        const probe = stat.probe
                        const disabled = !!key && (editorKey.disabledKeys || []).includes(key)
                        return (
                          <div key={`${index}-${key}`} className="rounded-lg border bg-slate-50/70 p-3 dark:bg-slate-950/50">
                            <div className="flex flex-col gap-3">
                              <div className="flex items-center gap-2">
                                <span className="badge bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-300">#{index}</span>
                                {disabled ? (
                                  <span className="badge bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-300">已禁用</span>
                                ) : stat.healthy === false ? (
                                  <span className="badge bg-rose-100 text-rose-700 dark:bg-rose-950 dark:text-rose-300">熔断</span>
                                ) : (
                                  <span className="badge bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300">可用</span>
                                )}
                                {key && (
                                  <span className="ml-auto truncate font-mono text-[11px] text-slate-500 dark:text-slate-400">
                                    {maskKeyLocal(key)}
                                  </span>
                                )}
                              </div>

                              <input
                                className="input input-sm font-mono"
                                value={key}
                                onChange={e => updateKeyRow(index, e.target.value)}
                                placeholder="sk-..."
                              />

                              {(stat.success_requests > 0 || stat.failure_requests > 0 || stat.last_latency_ms > 0 || probe) && (
                                <div className="flex flex-wrap gap-3 font-mono text-[11px] text-slate-500 dark:text-slate-400">
                                  <span>成功 {stat.success_requests || 0}</span>
                                  <span>失败 {stat.failure_requests || 0}</span>
                                  <span>延迟 {probe?.latency_ms ?? stat.last_latency_ms ?? 0}ms</span>
                                  {stat.consecutive_failure ? <span>连续失败 {stat.consecutive_failure}</span> : null}
                                </div>
                              )}

                              <div className="flex flex-wrap gap-2">
                                <button onClick={() => toggleKeyDisabled(index)} disabled={!key.trim()} className="btn-secondary btn-xs">
                                  {disabled ? '启用' : '禁用'}
                                </button>
                                <button onClick={() => handleProbeKeys(index)} disabled={!channel || !key.trim() || probingKeys} className="btn-secondary btn-xs">
                                  {probingKeys ? <RotateCcw className="h-3.5 w-3.5 animate-spin" /> : <Search className="h-3.5 w-3.5" />}
                                  探活
                                </button>
                                <button onClick={() => removeKeyRow(index)} className="btn-secondary btn-xs text-rose-600 dark:text-rose-400">
                                  <Trash2 className="h-3.5 w-3.5" />
                                  删除
                                </button>
                              </div>
                            </div>
                          </div>
                        )
                      })
                    )}
                  </div>
                </div>

                <div className="card p-5">
                  <h3 className="section-title">调度</h3>
                  <div className="mt-4 space-y-3">
                    <Field label="优先级（小值优先）">
                      <input className="input" type="number" value={priority} onChange={e => setPriority(Number(e.target.value))} min={1} />
                    </Field>
                    <Field label="权重（同级比例）">
                      <input className="input" type="number" value={weight} onChange={e => setWeight(Number(e.target.value))} min={1} />
                    </Field>
                  </div>
                </div>
              </aside>
            </div>
          </div>
        )}
      </section>

      {showModelPicker && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/45 p-4 backdrop-blur-sm" onClick={() => setShowModelPicker(false)}>
          <div className="flex max-h-[82vh] w-full max-w-3xl flex-col rounded-lg border bg-white shadow-2xl dark:bg-slate-900" onClick={e => e.stopPropagation()}>
            <div className="flex items-center justify-between border-b px-5 py-4">
              <div>
                <h3 className="text-base font-semibold text-slate-950 dark:text-slate-50">选择上游模型</h3>
                <p className="section-subtitle mt-1">从上游拉到 {pendingModels.length} 个模型</p>
              </div>
              <button onClick={() => setShowModelPicker(false)} className="btn-ghost p-2">
                <X className="h-4 w-4" />
              </button>
            </div>
            <div className="flex-1 overflow-y-auto p-4 scroll-thin">
              <div className="grid gap-2 md:grid-cols-2">
                {pendingModels.map(model => (
                  <label
                    key={model}
                    className={`flex cursor-pointer items-center gap-3 rounded-lg border px-3 py-2.5 text-sm transition-colors ${
                      pickerSelection[model]
                        ? 'border-blue-500 bg-blue-50 text-blue-600 dark:bg-blue-950/30 dark:text-blue-300'
                        : 'hover:border-slate-300 dark:hover:border-slate-600'
                    }`}
                  >
                    <input
                      type="checkbox"
                      className="h-4 w-4 rounded border-slate-300 text-blue-600 focus:ring-blue-600/20"
                      checked={!!pickerSelection[model]}
                      onChange={() => togglePickerItem(model)}
                    />
                    <span className="truncate font-mono text-xs">{model}</span>
                  </label>
                ))}
              </div>
            </div>
            <div className="flex items-center justify-between border-t px-5 py-4">
              <span className="text-xs text-slate-500 dark:text-slate-400">
                已选 {Object.values(pickerSelection).filter(Boolean).length} / {pendingModels.length}
              </span>
              <div className="flex gap-2">
                <button onClick={() => setShowModelPicker(false)} className="btn-secondary">取消</button>
                <button onClick={confirmModelSelection} className="btn-primary">应用选择</button>
              </div>
            </div>
          </div>
        </div>
      )}

      {showBulkAdd && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/45 p-4 backdrop-blur-sm animate-fade-in" onClick={() => setShowBulkAdd(false)}>
          <div className="flex max-h-[82vh] w-full max-w-lg flex-col rounded-2xl border bg-white shadow-2xl dark:bg-slate-900/90 dark:backdrop-blur-xl animate-scale-up" onClick={e => e.stopPropagation()}>
            <div className="flex items-center justify-between border-b px-5 py-4">
              <div>
                <h3 className="text-base font-semibold text-slate-950 dark:text-slate-50">批量导入 API Keys</h3>
                <p className="section-subtitle mt-1">支持每行一个 Key，或者用空格/逗号分隔</p>
              </div>
              <button onClick={() => setShowBulkAdd(false)} className="btn-ghost p-2">
                <X className="h-4 w-4" />
              </button>
            </div>
            <div className="flex-1 p-5">
              <textarea
                className="input min-h-[12rem] font-mono text-xs leading-5"
                placeholder="在此粘贴您的 Keys...&#10;sk-xxxxxx&#10;sk-yyyyyy"
                value={bulkText}
                onChange={e => setBulkText(e.target.value)}
              />
            </div>
            <div className="flex items-center justify-end gap-2 border-t px-5 py-4 bg-slate-50/50 dark:bg-slate-950/20">
              <button onClick={() => setShowBulkAdd(false)} className="btn-secondary">取消</button>
              <button onClick={handleBulkAddKeys} className="btn-primary">确认导入</button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
