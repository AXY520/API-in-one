import React, { useState, useEffect, useCallback } from 'react'
import { toast } from './Toast'
import { updateChannelKeys, probeChannelKeys, updateChannelKeyState, fetchChannelKeys } from '../api'

function maskKeyLocal(key) {
  if (!key || key.length <= 8) return '****'
  return key.slice(0, 4) + '****' + key.slice(-4)
}

export default function KeyModal({ channelName, isPersisted, initialKeys, models, onClose, onSaved }) {
  const keyStatsMap = {}
  ;(initialKeys.keyStats || []).forEach(s => { keyStatsMap[s.index] = s })
  const [rows, setRows] = useState(() => (initialKeys.keys || []).map((key, i) => ({
    index: i,
    key,
    disabled: (initialKeys.disabledKeys || []).includes(key),
    models: (initialKeys.keyModels || {})[String(i)] || [],
    stat: keyStatsMap[i] || {},
    probe: null,
  })))
  const [probeModel, setProbeModel] = useState('')
  const [probing, setProbing] = useState(false)
  const [saving, setSaving] = useState(false)

  const addRow = () => {
    setRows(prev => [...prev, { index: prev.length, key: '', disabled: false, models: [], stat: {}, probe: null }])
  }

  const removeRow = (i) => {
    setRows(prev => {
      const next = prev.filter((_, idx) => idx !== i)
      return next.map((r, idx) => ({ ...r, index: idx }))
    })
  }

  const updateRow = (i, field, value) => {
    setRows(prev => prev.map((r, idx) => idx === i ? { ...r, [field]: value } : r))
  }

  const handleSave = async () => {
    const valid = rows.filter(r => r.key.trim())
    if (valid.length === 0) {
      toast('至少需要一个有效的 Key', true)
      return
    }
    const keys = valid.map(r => r.key.trim())
    const disabledKeys = valid.filter(r => r.disabled).map(r => r.key.trim())
    const keyModels = {}
    valid.forEach(r => {
      if (r.key.trim() && r.models.length > 0) {
        keyModels[r.key.trim()] = r.models
      }
    })
    if (!isPersisted) {
      onSaved({ keys, disabledKeys, keyModels })
      return
    }
    setSaving(true)
    try {
      await updateChannelKeys(channelName, { keys, disabled_keys: disabledKeys, key_models: keyModels })
      toast('Keys 已保存')
      onSaved({ keys, disabledKeys, keyModels })
    } catch (e) {
      toast('保存 Keys 失败: ' + e.message, true)
    } finally {
      setSaving(false)
    }
  }

  const handleProbe = async (index = null) => {
    if (!isPersisted) {
      toast('请先保存渠道后再探活', true)
      return
    }
    if (!probeModel && rows.length > 0) {
      toast('请先选择探活模型', true)
      return
    }
    setProbing(true)
    try {
      const result = await probeChannelKeys(channelName, { model: probeModel, index })
      toast(`探活完成: ${result.model || probeModel}`)
      const results = result.results || []
      setRows(prev => prev.map(r => {
        const probe = results.find(p => p.index === r.index)
        return probe ? { ...r, probe } : r
      }))
    } catch (e) {
      toast('探活失败: ' + e.message, true)
    } finally {
      setProbing(false)
    }
  }

  // For display, not all models are available in this context
  // The probeModel will be set from the channel's first model

  useEffect(() => {
    if (!probeModel && models && models.length > 0) {
      setProbeModel(models[0])
    }
  }, [models])

  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal-content max-w-3xl" onClick={e => e.stopPropagation()}>
        <div className="sticky top-0 bg-white dark:bg-gray-900 border-b border-gray-200 dark:border-gray-800 px-6 py-4 flex items-center justify-between z-10">
          <div>
            <h2 className="text-lg font-bold text-gray-900 dark:text-gray-100">API Key 管理</h2>
            <p className="text-xs text-gray-400 mt-0.5">渠道: {channelName}</p>
          </div>
          <button onClick={onClose} className="btn-ghost p-1">
            <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" /></svg>
          </button>
        </div>

        <div className="p-6 space-y-4">
          <div className="flex items-center justify-between">
            <div>
              <p className="text-xs text-gray-400">{(rows.filter(r => r.key.trim())).length} 个 Key 已配置</p>
            </div>
            <div className="flex items-center gap-2">
              <select className="input input-sm font-mono w-44" value={probeModel} onChange={e => setProbeModel(e.target.value)}>
                {(!models || models.length === 0) && <option value="">选择模型</option>}
                {(models || []).map(m => <option key={m} value={m}>{m}</option>)}
              </select>
              <button onClick={() => handleProbe()} disabled={probing} className="btn-secondary btn-xs">
                {probing ? '探活中...' : '全局探活'}
              </button>
              <button onClick={addRow} className="btn-secondary btn-xs">+ 添加 Key</button>
            </div>
          </div>

          {rows.length === 0 ? (
            <div className="border border-dashed border-gray-300 dark:border-gray-700 rounded-xl p-8 text-center text-sm text-gray-400">
              还没有 Key，点击"添加 Key"开始
            </div>
          ) : (
            <div className="space-y-3 max-h-96 overflow-y-auto scroll-thin">
              {rows.map((row, i) => {
                const stat = row.stat || {}
                const isSuspended = row.disabled === false && stat.healthy === false
                let statusBadge = ''
                if (row.disabled) statusBadge = <span className="badge bg-gray-200 dark:bg-gray-700 text-gray-500 dark:text-gray-400">已禁用</span>
                else if (isSuspended) statusBadge = <span className="badge bg-red-100 dark:bg-red-950 text-red-700 dark:text-red-400">熔断</span>
                else statusBadge = <span className="badge bg-emerald-100 dark:bg-emerald-950 text-emerald-700 dark:text-emerald-400">健康</span>

                return (
                  <div key={i} className="p-4 bg-gray-50 dark:bg-gray-800/50 border border-gray-200 dark:border-gray-700 rounded-xl">
                    <div className="flex flex-col md:flex-row gap-3">
                      <div className="flex items-center gap-2 shrink-0">
                        <span className="badge font-mono bg-gray-200 dark:bg-gray-700 text-gray-600 dark:text-gray-400">#{row.index}</span>
                        {statusBadge}
                      </div>
                      <input
                        className="input input-sm font-mono flex-1"
                        value={row.key}
                        onChange={e => updateRow(i, 'key', e.target.value)}
                        placeholder="sk-..."
                      />
                      <div className="flex items-center gap-1 shrink-0">
                        <button onClick={() => updateRow(i, 'disabled', !row.disabled)} className={`btn-xs ${row.disabled ? 'btn-primary' : 'btn-secondary'}`}>
                          {row.disabled ? '启用' : '禁用'}
                        </button>
                        <button onClick={() => handleProbe(row.index)} className="btn-secondary btn-xs">探活</button>
                        <button onClick={() => removeRow(i)} className="btn-secondary btn-xs text-red-500 hover:text-red-600">删除</button>
                      </div>
                    </div>
                    {(stat.success_requests > 0 || stat.failure_requests > 0 || stat.last_latency_ms > 0) && (
                      <div className="mt-2 flex gap-4 text-[11px] font-mono text-gray-400">
                        <span>成功 {stat.success_requests || 0} / 失败 {stat.failure_requests || 0}{stat.consecutive_failure ? ` 连续失败 ${stat.consecutive_failure}` : ''}</span>
                        <span>延迟 {stat.last_latency_ms || 0}ms</span>
                        {stat.suspended_until && <span className="text-amber-500">冷却至 {stat.suspended_until}</span>}
                      </div>
                    )}
                    {row.probe && (
                      <div className={`mt-1 text-[11px] font-mono ${row.probe.ok ? 'text-emerald-500' : 'text-red-500'}`}>
                        探活: {row.probe.status || 'ERR'} / {row.probe.latency_ms}ms{row.probe.error ? ' - ' + row.probe.error : ''}
                      </div>
                    )}
                    {row.models.length > 0 && (
                      <div className="mt-2 flex flex-wrap gap-1">
                        {row.models.slice(0, 8).map(m => (
                          <span key={m} className="badge badge-sm bg-blue-100 dark:bg-blue-950 text-blue-600 dark:text-blue-400">{m}</span>
                        ))}
                        {row.models.length > 8 && <span className="badge badge-sm bg-blue-100 dark:bg-blue-950 text-blue-600 dark:text-blue-400">+{row.models.length - 8}</span>}
                      </div>
                    )}
                    {row.models.length === 0 && row.key.trim() && (
                      <div className="mt-1 text-[11px] text-gray-400">继承渠道全部模型</div>
                    )}
                  </div>
                )
              })}
            </div>
          )}
        </div>

        <div className="border-t border-gray-200 dark:border-gray-800 px-6 py-4 flex justify-end gap-2">
          <button onClick={onClose} className="btn-secondary">关闭</button>
          <button onClick={handleSave} disabled={saving} className="btn-primary">
            {saving ? '保存中...' : '保存 Keys'}
          </button>
        </div>
      </div>
    </div>
  )
}
