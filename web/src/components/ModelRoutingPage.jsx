import React, { useMemo, useState } from 'react'
import {
  ArrowDown,
  ArrowUp,
  CheckCircle2,
  CircleAlert,
  RefreshCcw,
  Save,
  Search,
  SlidersHorizontal,
} from 'lucide-react'
import { updateChannelRouting } from '../api'
import {
  channelCanServeModel,
  collectSchedulableModels,
  isModelDisabled,
  resolvedModel,
} from '../modelUtils'
import { toast } from './Toast'

function routeTone(row) {
  if (!row.enabled) return 'bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-300'
  if (row.disabled) return 'bg-amber-100 text-amber-700 dark:bg-amber-950 dark:text-amber-300'
  if (row.channel.healthy) return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300'
  return 'bg-rose-100 text-rose-700 dark:bg-rose-950 dark:text-rose-300'
}

function routeLabel(row) {
  if (!row.enabled) return '停用'
  if (row.disabled) return '模型停用'
  if (row.channel.healthy) return '可用'
  return '异常'
}

function sameSchedule(rows) {
  if (rows.length <= 1) return false
  const enabled = rows.filter(row => row.enabled && !row.disabled)
  if (enabled.length <= 1) return false
  const first = enabled[0]
  return enabled.every(row => row.priority === first.priority && row.weight === first.weight)
}

export default function ModelRoutingPage({ channels, refreshChannels }) {
  const models = useMemo(() => collectSchedulableModels(channels || []), [channels])
  const [query, setQuery] = useState('')
  const [selectedModel, setSelectedModel] = useState(() => models[0] || '')
  const [drafts, setDrafts] = useState({})
  const [saving, setSaving] = useState('')

  const currentModel = models.includes(selectedModel) ? selectedModel : (models[0] || '')
  const filteredModels = models.filter(model => model.toLowerCase().includes(query.trim().toLowerCase()))

  const rows = useMemo(() => {
    if (!currentModel) return []
    return (channels || [])
      .filter(channel => channelCanServeModel(channel, currentModel))
      .map(channel => {
        const draft = drafts[channel.name] || {}
        return {
          channel,
          resolved: resolvedModel(channel, currentModel),
          disabled: isModelDisabled(channel, currentModel),
          priority: Number(draft.priority ?? channel.priority ?? 10),
          weight: Number(draft.weight ?? channel.weight ?? 100),
          enabled: draft.enabled ?? (channel.enabled !== false),
          dirty:
            draft.priority !== undefined ||
            draft.weight !== undefined ||
            draft.enabled !== undefined,
        }
      })
      .sort((a, b) => {
        if (a.priority !== b.priority) return a.priority - b.priority
        if ((b.channel.healthy ? 1 : 0) !== (a.channel.healthy ? 1 : 0)) return (b.channel.healthy ? 1 : 0) - (a.channel.healthy ? 1 : 0)
        return a.channel.name.localeCompare(b.channel.name)
      })
  }, [channels, currentModel, drafts])

  const patchDraft = (name, patch) => {
    setDrafts(prev => ({
      ...prev,
      [name]: {
        ...prev[name],
        ...patch,
      },
    }))
  }

  const saveRow = async (row) => {
    const priority = Math.max(1, Number(row.priority) || 1)
    const weight = Math.max(1, Number(row.weight) || 1)
    setSaving(row.channel.name)
    try {
      await updateChannelRouting(row.channel.name, {
        priority,
        weight,
        enabled: row.enabled,
      })
      setDrafts(prev => {
        const next = { ...prev }
        delete next[row.channel.name]
        return next
      })
      await refreshChannels()
      toast('调度已保存')
    } catch (e) {
      toast('保存失败: ' + e.message, true)
    } finally {
      setSaving('')
    }
  }

  const saveAll = async () => {
    const dirtyRows = rows.filter(row => row.dirty)
    if (dirtyRows.length === 0) return
    setSaving('__all__')
    try {
      for (const row of dirtyRows) {
        await updateChannelRouting(row.channel.name, {
          priority: Math.max(1, Number(row.priority) || 1),
          weight: Math.max(1, Number(row.weight) || 1),
          enabled: row.enabled,
        })
      }
      setDrafts({})
      await refreshChannels()
      toast('调度已保存')
    } catch (e) {
      toast('保存失败: ' + e.message, true)
    } finally {
      setSaving('')
    }
  }

  const movePriority = (row, delta) => {
    patchDraft(row.channel.name, { priority: Math.max(1, Number(row.priority || 1) + delta) })
  }

  return (
    <div className="grid gap-4 lg:grid-cols-[20rem_minmax(0,1fr)]">
      <aside className="card flex min-h-[18rem] flex-col overflow-hidden lg:h-[calc(100vh-9rem)]">
        <div className="border-b p-4">
          <div className="flex items-center justify-between gap-3">
            <h2 className="section-title">模型</h2>
            <span className="badge bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-300">{models.length}</span>
          </div>
          <div className="relative mt-3">
            <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-400" />
            <input
              className="input input-sm pl-9"
              value={query}
              onChange={e => setQuery(e.target.value)}
              placeholder="搜索模型"
            />
          </div>
        </div>
        <div className="flex-1 overflow-y-auto p-2 scroll-thin">
          {filteredModels.length === 0 ? (
            <div className="rounded-lg border border-dashed p-6 text-center text-sm text-slate-400">没有匹配的模型</div>
          ) : filteredModels.map(model => {
            const active = model === currentModel
            return (
              <button
                key={model}
                onClick={() => setSelectedModel(model)}
                className={`mb-2 flex w-full items-center justify-between gap-3 rounded-lg border px-3 py-2.5 text-left transition-colors ${
                  active
                    ? 'border-blue-500 bg-blue-50/70 dark:bg-blue-950/30'
                    : 'bg-white hover:border-slate-300 hover:bg-slate-50 dark:bg-slate-900 dark:hover:bg-slate-800/70'
                }`}
              >
                <span className="min-w-0 truncate font-mono text-xs text-slate-900 dark:text-slate-100">{model}</span>
                <span className="badge badge-sm bg-slate-100 text-slate-500 dark:bg-slate-800 dark:text-slate-400">
                  {(channels || []).filter(channel => channelCanServeModel(channel, model)).length}
                </span>
              </button>
            )
          })}
        </div>
      </aside>

      <section className="space-y-4">
        <div className="card p-5">
          <div className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-2">
                <h2 className="truncate font-mono text-lg font-semibold text-slate-950 dark:text-slate-50">{currentModel || 'N/A'}</h2>
                {sameSchedule(rows) && <span className="badge bg-blue-100 text-blue-600 dark:bg-blue-950 dark:text-blue-300">同级轮询</span>}
              </div>
              <div className="mt-2 flex flex-wrap gap-2 text-xs text-slate-500 dark:text-slate-400">
                <span>{rows.length} 个渠道</span>
                <span>{rows.filter(row => row.channel.enabled !== false && !row.disabled).length} 个可调度</span>
              </div>
            </div>
            <button onClick={saveAll} disabled={saving === '__all__' || rows.every(row => !row.dirty)} className="btn-primary self-start">
              {saving === '__all__' ? <RefreshCcw className="h-4 w-4 animate-spin" /> : <Save className="h-4 w-4" />}
              保存全部
            </button>
          </div>
        </div>

        <div className="card overflow-hidden">
          <div className="overflow-x-auto">
            <table className="min-w-full text-sm">
              <thead className="bg-slate-50/70 text-xs uppercase text-slate-500 dark:bg-slate-950/50 dark:text-slate-400">
                <tr>
                  <th className="px-4 py-3 text-left">渠道</th>
                  <th className="px-4 py-3 text-left">上游模型</th>
                  <th className="px-4 py-3 text-left">状态</th>
                  <th className="px-4 py-3 text-left">优先级</th>
                  <th className="px-4 py-3 text-left">权重</th>
                  <th className="px-4 py-3 text-left">操作</th>
                </tr>
              </thead>
              <tbody>
                {rows.length === 0 ? (
                  <tr>
                    <td colSpan={6} className="px-4 py-14 text-center text-slate-400">没有可用渠道</td>
                  </tr>
                ) : rows.map(row => (
                  <tr key={row.channel.name} className="border-t">
                    <td className="px-4 py-3">
                      <div className="flex min-w-[12rem] items-center gap-2">
                        {row.channel.healthy ? <CheckCircle2 className="h-4 w-4 text-emerald-600" /> : <CircleAlert className="h-4 w-4 text-rose-600" />}
                        <span className="truncate font-medium text-slate-950 dark:text-slate-50">{row.channel.name}</span>
                        {row.channel.responses_only && <span className="badge badge-sm bg-violet-100 text-violet-700 dark:bg-violet-950 dark:text-violet-300">Responses</span>}
                      </div>
                    </td>
                    <td className="max-w-[22rem] px-4 py-3 font-mono text-xs text-slate-600 dark:text-slate-300">
                      <div className="truncate" title={row.resolved}>{row.resolved}</div>
                    </td>
                    <td className="px-4 py-3">
                      <button
                        onClick={() => patchDraft(row.channel.name, { enabled: !row.enabled })}
                        className={`badge ${routeTone(row)}`}
                      >
                        {row.enabled ? routeLabel(row) : '停用'}
                      </button>
                    </td>
                    <td className="px-4 py-3">
                      <div className="flex items-center gap-2">
                        <input
                          className="input input-sm w-20 font-mono"
                          type="number"
                          min={1}
                          value={row.priority}
                          onChange={e => patchDraft(row.channel.name, { priority: Number(e.target.value) })}
                        />
                        <div className="flex gap-1">
                          <button onClick={() => movePriority(row, -1)} className="btn-secondary btn-xs p-1.5" aria-label="提高优先级">
                            <ArrowUp className="h-3.5 w-3.5" />
                          </button>
                          <button onClick={() => movePriority(row, 1)} className="btn-secondary btn-xs p-1.5" aria-label="降低优先级">
                            <ArrowDown className="h-3.5 w-3.5" />
                          </button>
                        </div>
                      </div>
                    </td>
                    <td className="px-4 py-3">
                      <input
                        className="input input-sm w-24 font-mono"
                        type="number"
                        min={1}
                        value={row.weight}
                        onChange={e => patchDraft(row.channel.name, { weight: Number(e.target.value) })}
                      />
                    </td>
                    <td className="px-4 py-3">
                      <button onClick={() => saveRow(row)} disabled={!row.dirty || saving === row.channel.name || saving === '__all__'} className="btn-secondary btn-xs">
                        {saving === row.channel.name ? <RefreshCcw className="h-3.5 w-3.5 animate-spin" /> : <SlidersHorizontal className="h-3.5 w-3.5" />}
                        保存
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      </section>
    </div>
  )
}
