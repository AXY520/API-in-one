import React, { useEffect, useMemo, useRef, useState } from 'react'
import {
  Activity,
  CircleAlert,
  Clock3,
  RefreshCcw,
  Server,
  ShieldCheck,
  Trash2,
} from 'lucide-react'
import { Chart, registerables } from 'chart.js'
import { fetchStats, reloadConfig, clearLogs } from '../api'
import { collectSchedulableModels, groupModelsByProvider } from '../modelUtils'
import { toast } from './Toast'

Chart.register(...registerables)

function formatNumber(num) {
  return Number(num || 0).toLocaleString()
}

function formatLatency(ms) {
  return `${Math.round(Number(ms || 0)).toLocaleString()} ms`
}

function topChannels(channels = []) {
  return [...channels]
    .map(channel => {
      const stats = channel.key_stats || []
      const totalRequests = stats.reduce((sum, item) => sum + (item.total_requests || 0), 0)
      const avgLatency = stats.length > 0
        ? Math.round(stats.reduce((sum, item) => sum + (item.last_latency_ms || 0), 0) / stats.length)
        : 0
      return {
        ...channel,
        totalRequests,
        avgLatency,
        disabledModels: channel.disabled_models?.length || 0,
      }
    })
    .sort((a, b) => b.totalRequests - a.totalRequests || a.avgLatency - b.avgLatency)
}

export default function Dashboard({ channels, refreshAll }) {
  const [stats, setStats] = useState(null)
  const [busyAction, setBusyAction] = useState('')
  const pieRef = useRef(null)
  const barRef = useRef(null)
  const pieChart = useRef(null)
  const barChart = useRef(null)

  useEffect(() => {
    fetchStats().then(setStats).catch(() => {})
  }, [])

  useEffect(() => {
    const interval = setInterval(() => {
      fetchStats().then(setStats).catch(() => {})
    }, 5000)
    return () => clearInterval(interval)
  }, [])

  useEffect(() => {
    if (!stats || !pieRef.current) return
    const protocols = stats.protocols || {}
    const labels = Object.keys(protocols)
    const data = Object.values(protocols)
    if (pieChart.current) pieChart.current.destroy()
    if (labels.length === 0) {
      labels.push('无数据')
      data.push(1)
    }
    pieChart.current = new Chart(pieRef.current, {
      type: 'doughnut',
      data: {
        labels,
        datasets: [
          {
            data,
            backgroundColor: ['#0f766e', '#0ea5e9', '#f59e0b', '#8b5cf6', '#ef4444', '#64748b'],
            borderWidth: 0,
          },
        ],
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        cutout: '72%',
        plugins: {
          legend: {
            position: 'bottom',
            labels: {
              boxWidth: 10,
              padding: 14,
              color: document.documentElement.classList.contains('dark') ? '#cbd5e1' : '#475569',
              font: { size: 11 },
            },
          },
        },
      },
    })
    return () => {
      if (pieChart.current) pieChart.current.destroy()
    }
  }, [stats])

  useEffect(() => {
    if (!barRef.current) return
    const ranked = topChannels(channels).slice(0, 8)
    const labels = ranked.map(c => c.name)
    const latency = ranked.map(c => c.avgLatency)
    const requests = ranked.map(c => c.totalRequests)
    if (barChart.current) barChart.current.destroy()
    const isDark = document.documentElement.classList.contains('dark')
    const textColor = isDark ? '#94a3b8' : '#64748b'
    const gridColor = isDark ? 'rgba(148,163,184,0.12)' : 'rgba(100,116,139,0.15)'
    barChart.current = new Chart(barRef.current, {
      type: 'bar',
      data: {
        labels,
        datasets: [
          {
            label: '平均延迟 (ms)',
            data: latency,
            backgroundColor: 'rgba(15,118,110,0.16)',
            borderColor: '#0f766e',
            borderWidth: 1.5,
            borderRadius: 6,
            yAxisID: 'y',
          },
          {
            label: '调用次数',
            data: requests,
            backgroundColor: 'rgba(14,165,233,0.16)',
            borderColor: '#0ea5e9',
            borderWidth: 1.5,
            borderRadius: 6,
            yAxisID: 'y1',
          },
        ],
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        scales: {
          x: { grid: { display: false }, ticks: { color: textColor, font: { size: 11 } } },
          y: { position: 'left', grid: { color: gridColor }, ticks: { color: textColor } },
          y1: { position: 'right', grid: { drawOnChartArea: false }, ticks: { color: textColor } },
        },
        plugins: {
          legend: {
            labels: { color: textColor, boxWidth: 10, padding: 14, font: { size: 11 } },
          },
        },
      },
    })
    return () => {
      if (barChart.current) barChart.current.destroy()
    }
  }, [channels])

  const handleReload = async () => {
    setBusyAction('reload')
    try {
      await reloadConfig()
      toast('配置已重载')
      await refreshAll()
    } catch (e) {
      toast('重载失败: ' + e.message, true)
    } finally {
      setBusyAction('')
    }
  }

  const handleClearLogs = async () => {
    if (!window.confirm('确认清空所有请求日志？这玩意儿删了就没了。')) return
    setBusyAction('clear')
    try {
      await clearLogs()
      toast('日志已清除')
      setStats(null)
      fetchStats().then(setStats).catch(() => {})
    } catch (e) {
      toast('清除日志失败: ' + e.message, true)
    } finally {
      setBusyAction('')
    }
  }

  const total = stats?.total || 0
  const success = stats?.success || 0
  const successRate = total > 0 ? ((success / total) * 100).toFixed(1) : '100.0'
  const avgLatency = stats?.avg_duration_ms || 0
  const enabledChannels = channels.filter(c => c.enabled !== false)
  const healthyChannels = enabledChannels.filter(c => c.healthy)
  const unhealthyChannels = enabledChannels.filter(c => !c.healthy)
  const rankedChannels = useMemo(() => topChannels(channels), [channels])
  const enabledModelGroups = useMemo(() => {
    const models = collectSchedulableModels(channels || [])
    return groupModelsByProvider(models)
  }, [channels])
  const enabledModelCount = enabledModelGroups.reduce((sum, group) => sum + group.models.length, 0)

  const metricCards = [
    {
      label: '累计请求',
      value: formatNumber(total),
      description: '全部入口请求量',
      icon: Activity,
      borderColor: 'border-t-2 border-t-indigo-500/80 dark:border-t-indigo-400/80 shadow-indigo-500/5',
      iconBg: 'bg-indigo-50 text-indigo-600 dark:bg-indigo-950/30 dark:text-indigo-400',
      tone: 'text-slate-900 dark:text-slate-50',
    },
    {
      label: '成功率',
      value: `${successRate}%`,
      description: `${formatNumber(success)} 次成功转发`,
      icon: ShieldCheck,
      borderColor: 'border-t-2 border-t-emerald-500/80 dark:border-t-emerald-400/80 shadow-emerald-500/5',
      iconBg: 'bg-emerald-50 text-emerald-600 dark:bg-emerald-950/30 dark:text-emerald-400',
      tone: 'text-emerald-700 dark:text-emerald-400',
    },
    {
      label: '平均时延',
      value: formatLatency(avgLatency),
      description: '按请求耗时聚合',
      icon: Clock3,
      borderColor: 'border-t-2 border-t-amber-500/80 dark:border-t-amber-400/80 shadow-amber-500/5',
      iconBg: 'bg-amber-50 text-amber-600 dark:bg-amber-950/30 dark:text-amber-400',
      tone: 'text-amber-700 dark:text-amber-450',
    },
    {
      label: '可用渠道',
      value: `${healthyChannels.length} / ${enabledChannels.length || channels.length}`,
      description: unhealthyChannels.length > 0 ? `${unhealthyChannels.length} 个待处理` : '当前没有异常渠道',
      icon: Server,
      borderColor: unhealthyChannels.length > 0 
        ? 'border-t-2 border-t-rose-500/80 dark:border-t-rose-400/80 shadow-rose-500/5'
        : 'border-t-2 border-t-sky-500/80 dark:border-t-sky-400/80 shadow-sky-500/5',
      iconBg: unhealthyChannels.length > 0
        ? 'bg-rose-50 text-rose-600 dark:bg-rose-950/30 dark:text-rose-400'
        : 'bg-sky-50 text-sky-600 dark:bg-sky-950/30 dark:text-sky-400',
      tone: unhealthyChannels.length > 0 ? 'text-rose-700 dark:text-rose-400' : 'text-slate-900 dark:text-slate-50',
    },
  ]

  return (
    <div className="space-y-6">
      <section className="grid gap-4 xl:grid-cols-[1.5fr_1fr]">
        <div className="card p-5 lg:p-6">
          <div className="flex flex-col gap-5 lg:flex-row lg:items-start lg:justify-between">
            <div className="max-w-2xl">
              <h2 className="text-2xl font-extrabold tracking-tight text-slate-950 dark:text-slate-50">运行总览</h2>
            </div>
            <div className="grid gap-2 sm:grid-cols-2">
              <button onClick={handleReload} disabled={busyAction === 'reload'} className="btn-secondary justify-start shadow-sm">
                <RefreshCcw className={`h-4 w-4 ${busyAction === 'reload' ? 'animate-spin' : ''}`} />
                重载配置
              </button>
              <button onClick={handleClearLogs} disabled={busyAction === 'clear'} className="btn-secondary justify-start text-rose-600 dark:text-rose-400 shadow-sm">
                <Trash2 className="h-4 w-4" />
                清空日志
              </button>
            </div>
          </div>

          <div className="mt-6 grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
            {metricCards.map(item => {
              const Icon = item.icon
              return (
                <div key={item.label} className={`rounded-xl border bg-slate-50/40 p-4 dark:bg-slate-950/40 shadow-sm transition-all duration-300 hover:scale-[1.01] ${item.borderColor}`}>
                  <div className="flex items-start justify-between gap-3">
                    <div>
                      <div className="stat-label">{item.label}</div>
                      <div className={`stat-value mt-1.5 ${item.tone}`}>{item.value}</div>
                    </div>
                    <div className={`rounded-xl p-2.5 shadow-inner ${item.iconBg}`}>
                      <Icon className="h-4.5 w-4.5" />
                    </div>
                  </div>
                  <div className="mt-3.5 text-xs text-slate-400 dark:text-slate-500 font-medium">{item.description}</div>
                </div>
              )
            })}
          </div>
        </div>


        <div className="card p-5 lg:p-6">
          <div className="flex items-center justify-between">
            <div>
              <h3 className="section-title">异常提醒</h3>
            </div>
            <span className="badge bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-300">
              {unhealthyChannels.length} 条
            </span>
          </div>
          <div className="mt-4 space-y-3">
            {unhealthyChannels.length === 0 ? (
              <div className="rounded-lg border border-dashed p-5 text-sm text-slate-500 dark:text-slate-400">
                无异常渠道
              </div>
            ) : (
              unhealthyChannels.slice(0, 5).map(channel => (
                <div key={channel.name} className="rounded-lg border p-3">
                  <div className="flex items-center justify-between gap-3">
                    <div className="min-w-0">
                      <div className="truncate font-medium text-slate-900 dark:text-slate-100">{channel.name}</div>
                      <div className="mt-1 flex items-center gap-2 text-xs text-slate-500 dark:text-slate-400">
                        <span className="chip bg-rose-50 text-rose-700 dark:bg-rose-950/30 dark:text-rose-300">{channel.type}</span>
                        <span>已停用模型 {channel.disabled_models?.length || 0}</span>
                      </div>
                    </div>
                    <CircleAlert className="h-[18px] w-[18px] shrink-0 text-rose-500" />
                  </div>
                </div>
              ))
            )}
          </div>
        </div>
      </section>

      <section className="grid gap-4 xl:grid-cols-[1.3fr_1fr]">
        <div className="card p-5">
          <div className="flex items-center justify-between border-b pb-3">
            <div>
              <h3 className="section-title">热点渠道</h3>
              <p className="section-subtitle mt-1">按请求量排序，顺带看平均延迟</p>
            </div>
            <span className="text-xs text-slate-400">Top 8</span>
          </div>
          <div className="relative mt-4 h-80">
            <canvas ref={barRef} />
          </div>
        </div>

        <div className="card p-5">
          <div className="flex items-center justify-between border-b pb-3">
            <div>
              <h3 className="section-title">协议分布</h3>
              <p className="section-subtitle mt-1">入口协议占比</p>
            </div>
            <span className="text-xs text-slate-400">实时刷新</span>
          </div>
          <div className="relative mt-4 h-80">
            <canvas ref={pieRef} />
          </div>
        </div>
      </section>
      <section className="grid gap-4">
        <div className="card overflow-hidden">
          <div className="flex items-center justify-between border-b px-5 py-4">
            <div>
              <h3 className="section-title">启用模型</h3>
            </div>
            <span className="badge bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-300">
              {enabledModelCount} 个
            </span>
          </div>
          {enabledModelGroups.length === 0 ? (
            <div className="p-6 text-sm text-slate-400">没有启用模型</div>
          ) : (
            <div className="divide-y">
              {enabledModelGroups.map(group => (
                <div key={group.provider} className="grid gap-3 px-5 py-4 lg:grid-cols-[9rem_minmax(0,1fr)]">
                  <div className="flex items-center gap-2">
                    <span className="font-medium text-slate-900 dark:text-slate-100">{group.provider}</span>
                    <span className="badge badge-sm bg-slate-100 text-slate-500 dark:bg-slate-800 dark:text-slate-400">{group.models.length}</span>
                  </div>
                  <div className="flex flex-wrap gap-2">
                    {group.models.map(model => (
                      <span key={model} className="rounded-md border bg-slate-50 px-2 py-1 font-mono text-[11px] text-slate-700 dark:bg-slate-950/50 dark:text-slate-300">
                        {model}
                      </span>
                    ))}
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      </section>
      <section className="grid gap-4">
        <div className="card overflow-hidden">
          <div className="flex items-center justify-between border-b px-5 py-4">
            <div>
              <h3 className="section-title">渠道巡检列表</h3>
            </div>
          </div>
          <div className="overflow-x-auto">
            <table className="min-w-full text-sm">
              <thead className="bg-slate-50/70 text-xs uppercase text-slate-500 dark:bg-slate-950/50 dark:text-slate-400">
                <tr>
                  <th className="px-5 py-3 text-left">渠道</th>
                  <th className="px-5 py-3 text-left">状态</th>
                  <th className="px-5 py-3 text-left">请求量</th>
                  <th className="px-5 py-3 text-left">平均延迟</th>
                  <th className="px-5 py-3 text-left">模型</th>
                </tr>
              </thead>
              <tbody>
                {rankedChannels.length === 0 ? (
                  <tr>
                    <td colSpan={5} className="px-5 py-12 text-center text-slate-400">
                      还没有渠道数据。
                    </td>
                  </tr>
                ) : (
                  rankedChannels.slice(0, 10).map(channel => (
                    <tr key={channel.name} className="border-t">
                      <td className="px-5 py-4">
                        <div className="font-medium text-slate-900 dark:text-slate-100">{channel.name}</div>
                        <div className="mt-1 text-xs text-slate-500 dark:text-slate-400">{channel.base_url || '未配置地址'}</div>
                      </td>
                      <td className="px-5 py-4">
                        <span className={`badge ${channel.enabled === false ? 'bg-slate-100 text-slate-500 dark:bg-slate-800 dark:text-slate-300' : channel.healthy ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300' : 'bg-rose-100 text-rose-700 dark:bg-rose-950 dark:text-rose-300'}`}>
                          {channel.enabled === false ? '停用' : channel.healthy ? '健康' : '异常'}
                        </span>
                      </td>
                      <td className="px-5 py-4 font-mono text-slate-700 dark:text-slate-300">{formatNumber(channel.totalRequests)}</td>
                      <td className="px-5 py-4 font-mono text-slate-700 dark:text-slate-300">{formatLatency(channel.avgLatency)}</td>
                      <td className="px-5 py-4 text-slate-500 dark:text-slate-400">
                        {channel.models?.length || 0} 个
                      </td>
                    </tr>
                  ))
                )}
              </tbody>
            </table>
          </div>
        </div>
      </section>
    </div>
  )
}
