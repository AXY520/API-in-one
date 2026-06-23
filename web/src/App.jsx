import React, { useState, useEffect, useCallback } from 'react'
import { RefreshCcw, Server, ShieldAlert } from 'lucide-react'
import { setAdminKey, clearAdminKey, getAdminKey, fetchChannels, fetchSettings } from './api'
import Toast, { toast } from './components/Toast'
import LoginPage from './components/LoginPage'
import Sidebar, { SidebarToggle } from './components/Sidebar'
import Dashboard from './components/Dashboard'
import ChannelEditor from './components/ChannelEditor'
import ModelRoutingPage from './components/ModelRoutingPage'
import LogsPage from './components/LogsPage'
import SettingsPage from './components/SettingsPage'

const TABS = [
  { id: 'dashboard', label: '运行总览', shortLabel: '总览', icon: 'LayoutDashboard' },
  { id: 'channels', label: '渠道编排', shortLabel: '渠道', icon: 'Route' },
  { id: 'model-routing', label: '模型路由', shortLabel: '路由', icon: 'SlidersHorizontal' },
  { id: 'logs', label: '请求流水', shortLabel: '流水', icon: 'ListTree' },
  { id: 'settings', label: '凭据策略', shortLabel: '设置', icon: 'KeyRound' },
]

export default function App() {
  const [authenticated, setAuthenticated] = useState(false)
  const [loading, setLoading] = useState(true)
  const [activeTab, setActiveTab] = useState('dashboard')
  const [dark, setDark] = useState(() => localStorage.getItem('theme') !== 'light')
  const [channels, setChannels] = useState([])
  const [settings, setSettings] = useState(null)
  const [mobileNavOpen, setMobileNavOpen] = useState(false)
  const [refreshing, setRefreshing] = useState(false)

  useEffect(() => {
    document.documentElement.classList.toggle('dark', dark)
    localStorage.setItem('theme', dark ? 'dark' : 'light')
  }, [dark])

  const tryLogin = useCallback(async (key) => {
    setAdminKey(key)
    try {
      const [ch, st] = await Promise.all([fetchChannels(), fetchSettings()])
      setChannels(ch)
      setSettings(st)
      setAuthenticated(true)
      setLoading(false)
    } catch (e) {
      clearAdminKey()
      setLoading(false)
      throw e
    }
  }, [])

  useEffect(() => {
    const saved = getAdminKey()
    if (saved) {
      tryLogin(saved).catch(() => {
        clearAdminKey()
        setLoading(false)
      })
    } else {
      setLoading(false)
    }
  }, [tryLogin])

  const refreshChannels = useCallback(async () => {
    const ch = await fetchChannels()
    setChannels(ch)
    return ch
  }, [])

  const refreshSettings = useCallback(async () => {
    const st = await fetchSettings()
    setSettings(st)
    return st
  }, [])

  const refreshAll = useCallback(async () => {
    const [ch, st] = await Promise.all([refreshChannels(), refreshSettings()])
    return { channels: ch, settings: st }
  }, [refreshChannels, refreshSettings])

  const handleRefreshAll = async () => {
    setRefreshing(true)
    try {
      await refreshAll()
      toast('数据已刷新')
    } catch (e) {
      toast('刷新失败: ' + e.message, true)
    } finally {
      setRefreshing(false)
    }
  }

  if (loading) {
    return (
      <div className="app-shell flex min-h-screen items-center justify-center">
        <div className="h-9 w-9 animate-spin rounded-full border-2 border-blue-600 border-t-transparent" />
      </div>
    )
  }

  if (!authenticated) {
    return (
      <>
        <LoginPage onLogin={tryLogin} />
        <Toast />
      </>
    )
  }

  const handleLogout = () => {
    clearAdminKey()
    setAuthenticated(false)
  }

  const activeMeta = TABS.find(tab => tab.id === activeTab) || TABS[0]
  const enabledChannels = channels.filter(c => c.enabled !== false)
  const unhealthyChannels = enabledChannels.filter(c => !c.healthy)
  const accessKeyCount = settings?.access_keys?.length || 0

  return (
    <div className="app-shell min-h-screen">
      <Sidebar
        tabs={TABS}
        activeTab={activeTab}
        onTabChange={setActiveTab}
        dark={dark}
        onToggleTheme={() => setDark(d => !d)}
        onLogout={handleLogout}
        mobileOpen={mobileNavOpen}
        onMobileClose={() => setMobileNavOpen(false)}
      />
      <main className="min-h-screen lg:pl-64">
        <div className="sticky top-0 z-30 border-b bg-white/82 backdrop-blur-xl dark:bg-slate-950/82">
          <div className="mx-auto flex max-w-[1500px] items-center gap-3 px-4 py-3 lg:px-8">
            <SidebarToggle open={mobileNavOpen} onClick={() => setMobileNavOpen(true)} />
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-2">
                <h1 className="truncate text-lg font-semibold text-slate-950 dark:text-slate-50 lg:text-xl">{activeMeta.label}</h1>
                {unhealthyChannels.length > 0 && (
                  <span className="inline-flex items-center gap-1 rounded-md bg-amber-100 px-2 py-0.5 text-xs font-medium text-amber-800 dark:bg-amber-950 dark:text-amber-300">
                    <ShieldAlert className="h-3.5 w-3.5" />
                    {unhealthyChannels.length} 个渠道异常
                  </span>
                )}
              </div>
            </div>
            <div className="hidden items-center gap-2 rounded-lg border bg-slate-50 px-3 py-2 text-xs text-slate-600 dark:bg-slate-900 dark:text-slate-300 md:flex">
              <Server className="h-4 w-4 text-blue-600 dark:text-blue-400" />
              <span>{enabledChannels.length}/{channels.length} 渠道启用</span>
              <span className="text-slate-300 dark:text-slate-700">|</span>
              <span>{accessKeyCount} Access Key</span>
            </div>
            <button onClick={handleRefreshAll} disabled={refreshing} className="btn-secondary px-3 py-2">
              <RefreshCcw className={`h-4 w-4 ${refreshing ? 'animate-spin' : ''}`} />
              <span className="hidden sm:inline">刷新</span>
            </button>
          </div>
        </div>

        <div className="mx-auto max-w-[1500px] p-4 lg:p-8">
          {activeTab === 'dashboard' && (
            <Dashboard
              channels={channels}
              settings={settings}
              refreshAll={refreshAll}
              refreshChannels={refreshChannels}
            />
          )}
          {activeTab === 'channels' && (
            <ChannelEditor
              channels={channels}
              refreshChannels={refreshChannels}
            />
          )}
          {activeTab === 'model-routing' && (
            <ModelRoutingPage
              channels={channels}
              refreshChannels={refreshChannels}
            />
          )}
          {activeTab === 'logs' && (
            <LogsPage settings={settings} refreshSettings={refreshSettings} channels={channels} />
          )}
          {activeTab === 'settings' && (
            <SettingsPage
              settings={settings}
              refreshSettings={refreshSettings}
            />
          )}
        </div>
      </main>
      <Toast />
    </div>
  )
}
