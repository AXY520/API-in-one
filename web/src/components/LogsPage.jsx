import React, { useState, useEffect, useRef, useCallback, useMemo } from 'react'
import {
  Activity,
  ChevronLeft,
  ChevronRight,
  Clock3,
  Filter,
  RefreshCcw,
  Search,
  X,
} from 'lucide-react'
import { fetchLogs, fetchLog, getAdminKey } from '../api'
import { toast } from './Toast'

function formatJSONHighlight(obj) {
  if (!obj) return ''
  const jsonStr = JSON.stringify(obj, null, 2)
  const escaped = jsonStr
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
  
  return escaped.replace(/("(\\u[a-zA-Z0-9]{4}|\\[^u]|[^\\"])*"(\s*:)?|\b(true|false|null)\b|-?\d+(?:\.\d*)?(?:[eE][+-]?\d+)?)/g, function (match) {
    let cls = 'number'
    if (/^"/.test(match)) {
      if (/:$/.test(match)) {
        cls = 'key'
      } else {
        cls = 'string'
      }
    } else if (/true|false/.test(match)) {
      cls = 'boolean'
    } else if (/null/.test(match)) {
      cls = 'null'
    }
    if (cls === 'key') {
      return `<span class="key">${match.replace(/:$/, '')}</span>:`
    }
    return `<span class="${cls}">${match}</span>`
  })
}

function JSONViewer({ data, title }) {
  const [copied, setCopied] = useState(false)
  const formattedHtml = useMemo(() => formatJSONHighlight(data), [data])

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(JSON.stringify(data, null, 2))
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch {
      // noop
    }
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-2">
        <h3 className="section-title">{title}</h3>
        <button
          onClick={handleCopy}
          className="btn-secondary btn-xs font-medium py-1 px-2 text-[10px]"
        >
          {copied ? '已复制 ✓' : '复制 JSON'}
        </button>
      </div>
      <pre
        className="max-h-72 overflow-auto rounded-xl bg-slate-950 p-4 text-[11px] scroll-thin border border-slate-800/80"
        style={{ color: '#93c5fd' }}
      >
        <code className="json-highlight" dangerouslySetInnerHTML={{ __html: formattedHtml }} />
      </pre>
    </div>
  )
}

function PayloadComparator({ inbound, outbound }) {
  const [activeTab, setActiveTab] = useState('compare')

  const hasOutbound = !!outbound && JSON.stringify(inbound) !== JSON.stringify(outbound)

  return (
    <div className="border rounded-xl overflow-hidden bg-white dark:bg-slate-900 border-slate-200 dark:border-slate-800 shadow-sm">
      {/* Tabs Header */}
      <div className="flex border-b bg-slate-50/50 dark:bg-slate-950/20 border-slate-200 dark:border-slate-800 px-4">
        <button
          onClick={() => setActiveTab('compare')}
          className={`px-4 py-3 text-xs font-semibold border-b-2 -mb-[2px] transition-colors ${
            activeTab === 'compare'
              ? 'border-blue-500 text-blue-600 dark:text-blue-400'
              : 'border-transparent text-slate-500 hover:text-slate-700 dark:text-slate-400 dark:hover:text-slate-300'
          }`}
        >
          对比视图
        </button>
        <button
          onClick={() => setActiveTab('inbound')}
          className={`px-4 py-3 text-xs font-semibold border-b-2 -mb-[2px] transition-colors ${
            activeTab === 'inbound'
              ? 'border-blue-500 text-blue-600 dark:text-blue-400'
              : 'border-transparent text-slate-500 hover:text-slate-700 dark:text-slate-400 dark:hover:text-slate-300'
          }`}
        >
          原始请求 (Inbound)
        </button>
        {hasOutbound && (
          <button
            onClick={() => setActiveTab('outbound')}
            className={`px-4 py-3 text-xs font-semibold border-b-2 -mb-[2px] transition-colors ${
              activeTab === 'outbound'
                ? 'border-blue-500 text-blue-600 dark:text-blue-400'
                : 'border-transparent text-slate-500 hover:text-slate-700 dark:text-slate-400 dark:hover:text-slate-300'
            }`}
          >
            上游最终请求 (Outbound)
          </button>
        )}
      </div>

      {/* Tabs Content */}
      <div className="p-4 bg-white dark:bg-slate-900">
        {activeTab === 'compare' && (
          <div>
            {!hasOutbound ? (
              <div className="flex flex-col items-center justify-center py-6 text-center border border-dashed rounded-lg border-slate-200 dark:border-slate-800 bg-slate-50/50 dark:bg-slate-950/10">
                <span className="text-xl mb-1">⚡</span>
                <div className="text-xs font-semibold text-slate-800 dark:text-slate-200">原生协议透传 (Passthrough)</div>
                <div className="text-[10px] text-slate-500 dark:text-slate-400 mt-1 max-w-md">上游协议类型与入站类型完全一致，采用零开销透明代理直接转发。</div>
                <div className="mt-4 max-w-full w-full px-2 text-left">
                  <JSONViewer data={inbound} title="Payload 视图" />
                </div>
              </div>
            ) : (
              <div className="grid gap-6 md:grid-cols-2">
                <div className="min-w-0">
                  <JSONViewer data={inbound} title="Inbound 客户端原始请求" />
                </div>
                <div className="min-w-0">
                  <JSONViewer data={outbound} title="Outbound 上游最终请求" />
                </div>
              </div>
            )}
          </div>
        )}

        {activeTab === 'inbound' && (
          <JSONViewer data={inbound} title="客户端原始请求" />
        )}

        {activeTab === 'outbound' && hasOutbound && (
          <JSONViewer data={outbound} title="上游最终请求" />
        )}
      </div>
    </div>
  )
}

function compactTime(ts) {
  if (!ts) return ''
  const m = ts.match(/(\d{1,2}):(\d{2}):(\d{2})/)
  if (m) return `${m[1].padStart(2, '0')}:${m[2]}:${m[3]}`
  return ts.length > 8 ? ts.slice(-8) : ts
}

function accessKeyStyle(key) {
  const palette = [
    ['#ccfbf1', '#115e59'], ['#dbeafe', '#1d4ed8'], ['#fef3c7', '#92400e'],
    ['#fee2e2', '#991b1b'], ['#ede9fe', '#5b21b6'], ['#dcfce7', '#166534'],
  ]
  let hash = 0
  for (let i = 0; i < key.length; i++) hash = ((hash << 5) - hash + key.charCodeAt(i)) | 0
  const [bg, fg] = palette[Math.abs(hash) % palette.length]
  return { background: bg, color: fg }
}

function statusTone(status) {
  if (status === 102 || status === 0) return 'bg-amber-100 text-amber-700 dark:bg-amber-950 dark:text-amber-300'
  if (status >= 200 && status < 400) return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300'
  return 'bg-rose-100 text-rose-700 dark:bg-rose-950 dark:text-rose-300'
}

function modeLabel(mode) {
  return mode === 'converted' ? '转换' : '透传'
}

function SummaryCard({ icon: Icon, label, value, note }) {
  return (
    <div className="rounded-lg border bg-slate-50/70 p-4 dark:bg-slate-950/50">
      <div className="flex items-start justify-between gap-3">
        <div>
          <div className="stat-label">{label}</div>
          <div className="mt-1 text-xl font-semibold font-mono text-slate-950 dark:text-slate-50">{value}</div>
        </div>
        <div className="rounded-md bg-white p-2 shadow-sm dark:bg-slate-900">
          <Icon className="h-4 w-4 text-blue-600 dark:text-blue-300" />
        </div>
      </div>
      <div className="mt-3 text-xs text-slate-500 dark:text-slate-400">{note}</div>
    </div>
  )
}

export default function LogsPage({ settings, channels: propChannels }) {
  const [logs, setLogs] = useState([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [limit] = useState(30)
  const [filterProtocol, setFilterProtocol] = useState('')
  const [filterModel, setFilterModel] = useState('')
  const [filterChannel, setFilterChannel] = useState('')
  const [filterAccessKey, setFilterAccessKey] = useState('')
  const [filterStatus, setFilterStatus] = useState('')
  const [filterQuery, setFilterQuery] = useState('')
  const [detail, setDetail] = useState(null)
  const [loading, setLoading] = useState(false)
  const [detailBusy, setDetailBusy] = useState(false)
  const [autoRefresh, setAutoRefresh] = useState(true)
  const timerRef = useRef(null)
  const totalPages = Math.ceil(total / limit) || 1

  const loadLogs = useCallback(async () => {
    setLoading(true)
    try {
      const data = await fetchLogs({
        limit,
        page,
        protocol: filterProtocol,
        model: filterModel,
        channel: filterChannel,
        accessKey: filterAccessKey,
        status: filterStatus,
        q: filterQuery,
      })
      setLogs(data.logs || [])
      setTotal(data.filtered_total || 0)
    } catch {
      // noop
    } finally {
      setLoading(false)
    }
  }, [limit, page, filterProtocol, filterModel, filterChannel, filterAccessKey, filterStatus, filterQuery])

  useEffect(() => {
    loadLogs()
  }, [loadLogs])

  useEffect(() => {
    if (!autoRefresh) return
    timerRef.current = setInterval(() => {
      fetchLogs({
        limit,
        page,
        protocol: filterProtocol,
        model: filterModel,
        channel: filterChannel,
        accessKey: filterAccessKey,
        status: filterStatus,
        q: filterQuery,
      }).then(data => {
        setLogs(data.logs || [])
        setTotal(data.filtered_total || 0)
      }).catch(() => {})
    }, 3000)
    return () => clearInterval(timerRef.current)
  }, [autoRefresh, limit, page, filterProtocol, filterModel, filterChannel, filterAccessKey, filterStatus, filterQuery])

  const resetPage = () => setPage(1)

  const handleDetail = async (id) => {
    setDetailBusy(true)
    try {
      const data = await fetchLog(id)
      setDetail(data.log || null)
    } catch {
      // noop
    } finally {
      setDetailBusy(false)
    }
  }

  const handleExportLogs = async () => {
    try {
      const q = new URLSearchParams()
      q.set('format', 'jsonl')
      q.set('limit', 1000)
      if (filterProtocol) q.set('protocol', filterProtocol)
      if (filterModel) q.set('model', filterModel)
      if (filterChannel) q.set('channel', filterChannel)
      if (filterAccessKey) q.set('access_key', filterAccessKey)
      if (filterStatus) q.set('status', filterStatus)
      if (filterQuery) q.set('q', filterQuery)

      const resp = await fetch(`/admin/logs?${q.toString()}`, {
        headers: {
          Authorization: 'Bearer ' + getAdminKey(),
        }
      })
      if (!resp.ok) throw new Error('导出失败')
      const blob = await resp.blob()
      const url = window.URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = `api-in-one-logs-${new Date().toISOString().slice(0, 10)}.jsonl`
      document.body.appendChild(a)
      a.click()
      a.remove()
      window.URL.revokeObjectURL(url)
      toast('日志已导出')
    } catch (e) {
      toast('导出失败: ' + e.message, true)
    }
  }

  const handleDrillDown = (field, value) => {
    if (!value || value === 'N/A') return
    if (field === 'model') setFilterModel(value)
    if (field === 'channel') setFilterChannel(value)
    if (field === 'access_key') setFilterAccessKey(value)
    setPage(1)
    setDetail(null)
  }

  const models = (settings?.models || []).map(m => m.id || m)
  const accessKeys = (settings?.access_keys || []).map(k => typeof k === 'string' ? k : k.key)
  const channels = (propChannels || []).map(c => c.name)

  const summary = useMemo(() => {
    const ok = logs.filter(log => log.status >= 200 && log.status < 400).length
    const pending = logs.filter(log => log.status === 102 || log.status === 0).length
    const avg = logs.length ? Math.round(logs.reduce((sum, item) => sum + (item.duration || 0), 0) / logs.length) : 0
    return { ok, pending, avg }
  }, [logs])

  const activeFilters = [filterProtocol, filterModel, filterChannel, filterAccessKey, filterStatus, filterQuery].filter(Boolean).length

  return (
    <div className="space-y-6">
      <section className="card p-5 lg:p-6">
          <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <h2 className="text-2xl font-extrabold tracking-tight text-slate-950 dark:text-slate-50">请求流水</h2>
            </div>
            <div className="flex flex-wrap items-center gap-3">
              <label className="flex items-center gap-2 text-xs font-semibold text-slate-600 dark:text-slate-400 cursor-pointer select-none">
                <input
                  type="checkbox"
                  className="sr-only"
                  checked={autoRefresh}
                  onChange={e => setAutoRefresh(e.target.checked)}
                />
                <span className={`relative inline-flex h-5 w-9 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 focus:outline-none ${autoRefresh ? 'bg-indigo-65 text-indigo-600' : 'bg-gray-250 dark:bg-gray-700'}`}>
                  <span className={`pointer-events-none inline-block h-4 w-4 rounded-full bg-white shadow-md transform ring-0 transition-transform duration-200 ${autoRefresh ? 'translate-x-4 bg-indigo-600' : 'translate-x-0 bg-gray-400 dark:bg-gray-500'}`} />
                </span>
                <span>{autoRefresh ? '自动刷新开启' : '自动刷新暂停'}</span>
              </label>

              <button onClick={handleExportLogs} className="btn-secondary py-1.5 px-3 btn-sm shadow-sm text-indigo-600 dark:text-indigo-400">
                <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2.2}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4" />
                </svg>
                导出 JSONL
              </button>

              <button onClick={loadLogs} disabled={loading} className="btn-secondary py-1.5 px-3 btn-sm shadow-sm">
                <RefreshCcw className={`h-4 w-4 ${loading ? 'animate-spin' : ''}`} />
                立即刷新
              </button>
            </div>
          </div>

          <div className="mt-6 grid gap-3 sm:grid-cols-3">
            <SummaryCard icon={Activity} label="当前页请求" value={logs.length.toLocaleString()} note={`${total.toLocaleString()} 条匹配记录`} />
            <SummaryCard icon={Clock3} label="平均耗时" value={`${summary.avg.toLocaleString()} ms`} note={`${summary.pending} 条处理中`} />
            <SummaryCard icon={Filter} label="筛选条件" value={String(activeFilters)} note={activeFilters ? '已启用筛选' : '当前查看全部'} />
          </div>
      </section>

      <section className="card overflow-hidden">
        <div className="border-b p-4">
          <div className="flex flex-wrap items-center gap-3">
            <select className="select select-sm w-36" value={filterProtocol} onChange={e => { setFilterProtocol(e.target.value); resetPage() }}>
              <option value="">全部协议</option>
              <option value="openai">OpenAI</option>
              <option value="claude-inbound">Claude</option>
              <option value="gemini-inbound">Gemini</option>
              <option value="responses">Responses</option>
            </select>
            <select className="select select-sm w-40" value={filterModel} onChange={e => { setFilterModel(e.target.value); resetPage() }}>
              <option value="">全部模型</option>
              {models.map(m => <option key={m} value={m}>{m}</option>)}
            </select>
            <select className="select select-sm w-40" value={filterChannel} onChange={e => { setFilterChannel(e.target.value); resetPage() }}>
              <option value="">全部渠道</option>
              {channels.map(name => <option key={name} value={name}>{name}</option>)}
            </select>
            <select className="select select-sm w-40" value={filterAccessKey} onChange={e => { setFilterAccessKey(e.target.value); resetPage() }}>
              <option value="">全部令牌</option>
              {accessKeys.map(k => <option key={k} value={k}>{k.slice(0, 4) + '****' + k.slice(-4)}</option>)}
            </select>
            <select className="select select-sm w-32" value={filterStatus} onChange={e => { setFilterStatus(e.target.value); resetPage() }}>
              <option value="">全部状态</option>
              <option value="success">成功</option>
              <option value="error">错误</option>
              <option value="429">429</option>
              <option value="502">502</option>
            </select>
            <div className="relative min-w-[14rem] flex-1">
              <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-400" />
              <input
                className="input input-sm pl-9"
                placeholder="搜索模型、渠道、错误..."
                value={filterQuery}
                onChange={e => setFilterQuery(e.target.value)}
                onKeyDown={e => e.key === 'Enter' && resetPage()}
              />
            </div>
          </div>
        </div>

        <div className="overflow-x-auto">
          <table className="min-w-full text-sm">
            <thead className="bg-slate-50/70 text-xs uppercase text-slate-500 dark:bg-slate-950/50 dark:text-slate-400">
              <tr>
                <th className="px-4 py-3 text-left">时间</th>
                <th className="px-4 py-3 text-left">访问 Key</th>
                <th className="px-4 py-3 text-left">协议链路</th>
                <th className="px-4 py-3 text-left">模型</th>
                <th className="px-4 py-3 text-left">渠道</th>
                <th className="px-4 py-3 text-left">状态</th>
                <th className="px-4 py-3 text-left">操作</th>
              </tr>
            </thead>
            <tbody>
              {logs.length === 0 ? (
                <tr>
                  <td colSpan={7} className="px-4 py-14 text-center text-slate-400">没有匹配的流水记录</td>
                </tr>
              ) : logs.map(log => {
                const displayChannel = (() => {
                  if (log.channel) return log.channel
                  const names = (log.attempts || []).map(a => a.channel).filter(Boolean)
                  if (names.length === 0) return 'N/A'
                  return names.length > 2 ? `${names[0]} / ${names[1]} +${names.length - 2}` : names.join(' / ')
                })()
                const key = log.access_key || 'N/A'
                const isPending = log.status === 102 || log.status === 0
                return (
                  <tr
                    key={log.id}
                    className="cursor-pointer border-t hover:bg-slate-50/80 dark:hover:bg-slate-900/60"
                    onClick={() => handleDetail(log.id)}
                  >
                    <td className="px-4 py-3 font-mono text-xs text-slate-500 dark:text-slate-400">{compactTime(log.timestamp)}</td>
                    <td className="px-4 py-3">
                      <span className="badge font-mono text-[10px]" style={accessKeyStyle(key)}>{key}</span>
                    </td>
                    <td className="px-4 py-3">
                      <div className="flex items-center space-x-1 text-xs">
                        <span className="badge font-semibold bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-200">
                          {(() => {
                            const p = log.protocol || '';
                            if (p.endsWith('-inbound')) return p.slice(0, -8).toUpperCase();
                            return p.toUpperCase();
                          })()}
                        </span>
                        
                        <div className="flex items-center -space-x-1 px-0.5">
                          <span className="text-[10px] text-slate-400 font-mono">─</span>
                          <span className={`px-1 py-0.5 rounded text-[9px] font-bold tracking-tight shadow-sm scale-90 ${
                            log.mode === 'converted'
                              ? 'bg-indigo-500 text-white dark:bg-indigo-600'
                              : 'bg-emerald-500 text-white dark:bg-emerald-600'
                          }`}>
                            {log.mode === 'converted' ? '转换' : '透传'}
                          </span>
                          <span className="text-[10px] text-slate-400 font-mono">{"─>"}</span>
                        </div>
                        
                        <span className="badge font-semibold bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-200">
                          {(() => {
                            const finalAttempt = log.attempts?.[log.attempts.length - 1];
                            const out = finalAttempt?.upstream_protocol || finalAttempt?.protocol || log.protocol || '';
                            if (out.endsWith('-inbound')) return out.slice(0, -8).toUpperCase();
                            return out.toUpperCase();
                          })()}
                        </span>
                      </div>
                    </td>
                    <td className="max-w-[18rem] px-4 py-3 font-mono text-xs text-slate-800 dark:text-slate-200">
                      <div className="truncate">{log.model}</div>
                    </td>
                    <td className="max-w-[14rem] px-4 py-3 text-xs text-slate-500 dark:text-slate-400">
                      <div className="truncate" title={displayChannel}>{displayChannel}</div>
                    </td>
                    <td className="px-4 py-3">
                      <div className="flex items-center gap-2">
                        <span className={`badge ${statusTone(log.status)}`}>{isPending ? '处理中' : log.status}</span>
                        {!isPending && <span className="font-mono text-xs text-slate-500 dark:text-slate-400">{log.duration}ms</span>}
                      </div>
                    </td>
                    <td className="px-4 py-3 text-xs font-medium text-blue-600 dark:text-blue-300">详情</td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>

        <div className="flex items-center justify-between border-t px-4 py-3">
          <button className="btn-secondary btn-xs" onClick={() => setPage(p => Math.max(1, p - 1))} disabled={page <= 1}>
            <ChevronLeft className="h-3.5 w-3.5" />
            上一页
          </button>
          <span className="font-mono text-xs text-slate-500 dark:text-slate-400">{page} / {totalPages}</span>
          <button className="btn-secondary btn-xs" onClick={() => setPage(p => Math.min(totalPages, p + 1))} disabled={page >= totalPages}>
            下一页
            <ChevronRight className="h-3.5 w-3.5" />
          </button>
        </div>
      </section>

      {detail && (
        <div className="modal-overlay" onClick={() => setDetail(null)}>
          <div className="modal-content max-w-5xl" onClick={e => e.stopPropagation()}>
            <div className="sticky top-0 z-10 flex items-center justify-between border-b bg-white px-6 py-4 dark:bg-slate-900">
              <div>
                <h2 className="text-lg font-semibold text-slate-950 dark:text-slate-50">日志详情</h2>
                <div className="mt-1 text-xs font-mono text-slate-500 dark:text-slate-400">Log ID: {detail.id}</div>
              </div>
              <button onClick={() => setDetail(null)} className="btn-ghost p-2">
                <X className="h-4 w-4" />
              </button>
            </div>
            <div className="space-y-6 p-6">
              <div className="grid gap-3 sm:grid-cols-2 md:grid-cols-4">
                {(() => {
                  const attempts = detail.attempts || []
                  const finalAttempt = attempts.length > 0 ? attempts[attempts.length - 1] : null
                  const finalMode = finalAttempt?.conversion_mode || detail.mode
                  const fields = [
                    { label: '状态', value: detail.status },
                    { label: '总耗时', value: `${detail.duration || 0} ms` },
                    { label: '尝试次数', value: `${attempts.length}` },
                    { label: '时间', value: detail.timestamp || '' },
                    { label: '入口协议', value: detail.protocol || '' },
                    { label: '转发模式', value: `${modeLabel(finalMode)}${detail.stream ? ' / stream' : ''}` },
                    { label: '上游协议', value: finalAttempt?.upstream_protocol || finalAttempt?.protocol || '' },
                    { label: '上游 URL', value: finalAttempt?.upstream_url || '', span: 2 },
                    { label: '请求模型', value: detail.model || '', clickField: 'model', rawVal: detail.model },
                    { label: '上游模型', value: detail.resolved_model || detail.model || '' },
                    { label: '最终渠道', value: detail.channel || finalAttempt?.channel || 'N/A', clickField: 'channel', rawVal: detail.channel || finalAttempt?.channel },
                    { label: '访问 Key', value: detail.access_key || '', clickField: 'access_key', rawVal: detail.access_key },
                    { label: '上游 Key', value: finalAttempt?.masked_key || '' },
                    { label: '适配器', value: finalAttempt?.adaptor || '' },
                  ]
                  return fields.map((f) => {
                    const isClickable = f.clickField && f.rawVal && f.rawVal !== 'N/A'
                    return (
                      <div
                        key={f.label}
                        className={`rounded-lg border bg-slate-50/70 p-3 dark:bg-slate-950/50 transition-colors ${
                          f.span === 2 ? 'md:col-span-2' : ''
                        } ${isClickable ? 'hover:bg-indigo-50/50 dark:hover:bg-indigo-950/10 cursor-pointer border-indigo-100 dark:border-indigo-900/30' : ''}`}
                        onClick={() => isClickable && handleDrillDown(f.clickField, f.rawVal)}
                      >
                        <div className="flex items-center justify-between">
                          <div className="text-[10px] font-semibold uppercase text-slate-500 dark:text-slate-400">{f.label}</div>
                          {isClickable && (
                            <span className="text-[9px] text-indigo-500 dark:text-indigo-400 font-medium">点击筛选 ⌕</span>
                          )}
                        </div>
                        <div className={`mt-1 break-words font-mono text-xs font-semibold ${isClickable ? 'text-indigo-600 dark:text-indigo-400' : 'text-slate-950 dark:text-slate-50'}`}>
                          {f.value || 'N/A'}
                        </div>
                      </div>
                    )
                  })
                })()}
              </div>

              {detail.error && (
                <div className="rounded-lg border border-rose-200 bg-rose-50 p-4 font-mono text-xs leading-6 text-rose-700 dark:border-rose-900 dark:bg-rose-950/30 dark:text-rose-300">
                  {detail.error}
                </div>
              )}

              {(detail.attempts || []).length > 0 && (
                <div>
                  <h3 className="section-title">转发 attempts</h3>
                  <div className="mt-3 space-y-3">
                    {detail.attempts.map((att, i) => {
                      const ok = att.status >= 200 && att.status < 400
                      return (
                        <div key={i} className="rounded-lg border p-4">
                          <div className="flex flex-wrap items-center justify-between gap-2">
                            <div className="font-mono text-sm text-slate-900 dark:text-slate-100">
                              <span className="font-semibold">#{att.attempt}</span> {att.channel || 'N/A'} {att.model || ''}
                            </div>
                            <span className={`badge ${ok ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300' : 'bg-rose-100 text-rose-700 dark:bg-rose-950 dark:text-rose-300'}`}>
                              {att.status || 'ERROR'} / {att.duration_ms || 0}ms
                            </span>
                          </div>
                          <div className="mt-2 flex flex-wrap gap-3 font-mono text-[11px] text-slate-500 dark:text-slate-400">
                            <span>Key #{att.key_index ?? 'N/A'}</span>
                            <span>{att.masked_key || 'N/A'}</span>
                            <span>{`${att.inbound_protocol || detail.protocol || 'N/A'} -> ${att.upstream_protocol || att.protocol || 'N/A'}`}</span>
                            <span>{modeLabel(att.conversion_mode || detail.mode)}</span>
                            <span>{att.adaptor || 'N/A'}</span>
                            <span>{att.retryable ? '可重试' : '不重试'}</span>
                          </div>
                          {att.upstream_url && (
                            <div className="mt-2 break-all rounded-md bg-slate-50 px-3 py-2 font-mono text-[11px] text-slate-600 dark:bg-slate-950 dark:text-slate-300">
                              {att.upstream_url}
                            </div>
                          )}
                          {att.error && (
                            <div className="mt-2 font-mono text-[11px] leading-6 text-rose-600 dark:text-rose-300">{att.error}</div>
                          )}
                        </div>
                      )
                    })}
                  </div>
                </div>
              )}

              {detail.request && (
                <PayloadComparator inbound={detail.request} outbound={detail.upstream_request} />
              )}
            </div>
            <div className="flex justify-end border-t px-6 py-4">
              <button onClick={() => setDetail(null)} className="btn-secondary">
                关闭
              </button>
            </div>
          </div>
        </div>
      )}

      {detailBusy && !detail && (
        <div className="fixed bottom-4 left-1/2 z-50 -translate-x-1/2 rounded-lg border bg-white px-4 py-2 text-sm text-slate-600 shadow-lg dark:bg-slate-900 dark:text-slate-300">
          正在拉日志详情...
        </div>
      )}
    </div>
  )
}
