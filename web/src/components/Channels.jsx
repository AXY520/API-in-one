import React, { useState } from 'react'
import { setChannelEnabled, deleteChannel } from '../api'
import { toast } from './Toast'
import ChannelModal from './ChannelModal'

function maskKey(key) {
  if (!key || key.length <= 8) return '****'
  return key.slice(0, 4) + '****' + key.slice(-4)
}

function healthStyle(stat) {
  if (!stat) return ''
  if (stat.disabled) return 'border-red-300 dark:border-red-700 bg-red-50 dark:bg-red-950/30 text-red-700 dark:text-red-400'
  if ((stat.consecutive_failure || 0) > 0) return 'border-amber-300 dark:border-amber-700 bg-amber-50 dark:bg-amber-950/30 text-amber-700 dark:text-amber-400'
  if ((stat.success_requests || 0) > 0) return 'border-emerald-300 dark:border-emerald-700 bg-emerald-50 dark:bg-emerald-950/30 text-emerald-700 dark:text-emerald-400'
  return 'border-gray-300 dark:border-gray-600 bg-gray-50 dark:bg-gray-800 text-gray-600 dark:text-gray-400'
}

export default function Channels({ channels, refreshChannels }) {
  const [modalOpen, setModalOpen] = useState(false)
  const [editChannel, setEditChannel] = useState(null)

  const handleToggle = async (name, enabled) => {
    try {
      await setChannelEnabled(name, enabled)
      toast(`渠道 ${name} 已${enabled ? '启用' : '禁用'}`)
      await refreshChannels()
    } catch (e) {
      toast('切换状态失败: ' + e.message, true)
    }
  }

  const handleDelete = async (name) => {
    if (!window.confirm(`确认永久删除渠道 "${name}" 吗？`)) return
    try {
      await deleteChannel(name)
      toast(`渠道 ${name} 已删除`)
      await refreshChannels()
    } catch (e) {
      toast('删除失败: ' + e.message, true)
    }
  }

  const openAdd = () => {
    setEditChannel(null)
    setModalOpen(true)
  }

  const openEdit = (ch) => {
    setEditChannel(ch)
    setModalOpen(true)
  }

  const handleSaved = async () => {
    setModalOpen(false)
    setEditChannel(null)
    await refreshChannels()
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-gray-900 dark:text-gray-100">渠道管理</h1>
          <p className="text-sm text-gray-500 dark:text-gray-400 mt-0.5">管理所有上游 API 路由渠道</p>
        </div>
        <button onClick={openAdd} className="btn-primary">
          <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M12 4.5v15m7.5-7.5h-15" /></svg>
          添加渠道
        </button>
      </div>

      {channels.length === 0 ? (
        <div className="card p-12 text-center">
          <svg className="w-12 h-12 mx-auto text-gray-300 dark:text-gray-600 mb-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M9 20l-5.447-2.724A1 1 0 013 16.382V5.618a1 1 0 011.447-.894L9 7m0 13l6-3m-6 3V7m6 10l4.553 2.276A1 1 0 0021 18.382V7.618a1 1 0 00-.553-.894L15 4m0 13V4m0 0L9 7" />
          </svg>
          <p className="text-gray-500 dark:text-gray-400">还没有配置任何渠道</p>
          <button onClick={openAdd} className="btn-primary mt-4">添加第一个渠道</button>
        </div>
      ) : (
        <div className="space-y-3">
          {channels.map(ch => {
            const isEnabled = ch.enabled
            const isHealthy = ch.healthy
            let statusColor = 'text-red-600 dark:text-red-400 bg-red-50 dark:bg-red-950/30 border-red-200 dark:border-red-800'
            let statusText = '熔断'
            if (!isEnabled) {
              statusColor = 'text-gray-500 dark:text-gray-400 bg-gray-100 dark:bg-gray-800 border-gray-200 dark:border-gray-700'
              statusText = '停用'
            } else if (isHealthy) {
              statusColor = 'text-emerald-600 dark:text-emerald-400 bg-emerald-50 dark:bg-emerald-950/30 border-emerald-200 dark:border-emerald-800'
              statusText = '在线'
            }
            const modelStats = {}
            ;(ch.model_stats || []).forEach(s => { modelStats[s.model] = s })

            return (
              <div key={ch.name} className="card p-5 hover:shadow-md transition-shadow">
                <div className="flex flex-col lg:flex-row lg:items-center gap-4">
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2 mb-2">
                      <span className={`inline-flex items-center gap-1 px-2 py-0.5 text-xs font-semibold rounded-md border ${statusColor}`}>
                        <span className={`w-1.5 h-1.5 rounded-full ${isEnabled && isHealthy ? 'bg-emerald-500' : !isEnabled ? 'bg-gray-400' : 'bg-red-500'}`} />
                        {statusText}
                      </span>
                      <span className="chip border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-800 text-gray-500 dark:text-gray-400">{ch.type}</span>
                    </div>
                    <h3 className="text-base font-semibold text-gray-900 dark:text-gray-100 truncate">{ch.name}</h3>
                    <p className="text-xs font-mono text-gray-400 dark:text-gray-500 mt-0.5 truncate">{ch.base_url}</p>
                  </div>

                  <div className="flex-1 min-w-0">
                    <div className="flex items-center justify-between mb-2">
                      <span className="text-xs font-semibold text-gray-400">{ch.models.length} 个模型</span>
                      <span className="text-xs font-mono text-gray-400">P{ch.priority} / W{ch.weight}</span>
                    </div>
                    <div className="flex items-center gap-1.5 overflow-hidden max-w-full">
                      {ch.models.length === 0 ? (
                        <span className="text-xs text-gray-400 italic whitespace-nowrap">未配置模型</span>
                      ) : (
                        <>
                          {ch.models.slice(0, 4).map(model => {
                            const stat = modelStats[model] || {}
                            return (
                              <span
                                key={model}
                                className={`chip text-[11px] whitespace-nowrap ${healthStyle(stat)}`}
                                title={`${model} | 成功 ${stat.success_requests || 0} / 失败 ${stat.failure_requests || 0}${stat.consecutive_failure ? ' / 连续失败 ' + stat.consecutive_failure : ''}${stat.last_error ? ' / ' + stat.last_error : ''}`}
                              >
                                {stat.disabled ? `${model} · 禁用` : model}
                              </span>
                            )
                          })}
                          {ch.models.length > 4 && (
                            <span className="chip border-blue-200 dark:border-blue-600 bg-blue-50 dark:bg-blue-950/30 text-blue-600 dark:text-blue-400 text-[11px] whitespace-nowrap">+{ch.models.length - 4}</span>
                          )}
                        </>
                      )}
                    </div>
                  </div>

                  <div className="flex items-center gap-4 shrink-0">
                    <div className="flex flex-wrap gap-1.5">
                      {ch.supports_responses && <span className="chip border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-800 text-gray-500 dark:text-gray-400 text-[11px]">Responses</span>}
                      {ch.disable_mimo_compat && <span className="chip border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-800 text-gray-500 dark:text-gray-400 text-[11px]">MiMo</span>}
                      <span className="chip border-gray-200 dark:border-gray-700 bg-gray-50 dark:bg-gray-800 text-gray-500 dark:text-gray-400 text-[11px]">Keys {ch.key_count}</span>
                      {Object.keys(ch.model_mapping || {}).length > 0 && (
                        <span className="chip border-purple-200 dark:border-purple-700 bg-purple-50 dark:bg-purple-950/30 text-purple-600 dark:text-purple-400 text-[11px]">映射 {Object.keys(ch.model_mapping).length}</span>
                      )}
                    </div>
                    <div className="flex items-center gap-2">
                      <label className="relative inline-flex items-center cursor-pointer">
                        <input type="checkbox" className="sr-only peer" checked={ch.enabled} onChange={() => handleToggle(ch.name, !ch.enabled)} />
                        <div className="w-9 h-5 bg-gray-200 dark:bg-gray-700 peer-focus:outline-none peer-focus:ring-2 peer-focus:ring-blue-500/30 rounded-full peer peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-gray-300 dark:after:border-gray-600 after:border after:rounded-full after:h-4 after:w-4 after:transition-all peer-checked:bg-blue-600" />
                      </label>
                      <button onClick={() => openEdit(ch)} className="btn-secondary btn-xs">编辑</button>
                      <button onClick={() => handleDelete(ch.name)} className="btn-secondary btn-xs text-red-600 dark:text-red-400">删除</button>
                    </div>
                  </div>
                </div>
              </div>
            )
          })}
        </div>
      )}

      {modalOpen && (
        <ChannelModal
          channel={editChannel}
          onClose={() => { setModalOpen(false); setEditChannel(null) }}
          onSaved={handleSaved}
        />
      )}
    </div>
  )
}
