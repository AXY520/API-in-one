import React from 'react'
import {
  LayoutDashboard,
  Route,
  ListTree,
  KeyRound,
  SlidersHorizontal,
  MoonStar,
  SunMedium,
  PanelLeftClose,
  PanelLeftOpen,
  LogOut,
  Activity,
  X,
} from 'lucide-react'

const icons = {
  LayoutDashboard,
  Route,
  ListTree,
  KeyRound,
  SlidersHorizontal,
}

function NavContent({ tabs, activeTab, onTabChange, dark, onToggleTheme, onLogout, onClose }) {
  return (
    <div className="flex h-full w-full flex-col">
      <div className="flex items-center justify-between px-6 py-5 border-b border-slate-100 dark:border-slate-800/80">
        <div className="flex items-center gap-3 min-w-0">
          <div className="flex h-10 w-10 items-center justify-center rounded-xl bg-gradient-to-tr from-indigo-600 to-violet-500 text-white shadow-md shadow-indigo-600/20">
            <Activity className="h-5 w-5" />
          </div>
          <div className="min-w-0">
            <div className="truncate text-sm font-extrabold text-slate-900 dark:text-slate-50 tracking-wide">API-in-one</div>
            <div className="text-[10px] text-slate-400 font-medium">API Gateway</div>
          </div>
        </div>
        {onClose && (
          <button onClick={onClose} className="btn-ghost p-1.5 lg:hidden" aria-label="关闭导航">
            <X className="h-4 w-4" />
          </button>
        )}
      </div>

      <nav className="flex-1 px-3 py-4 space-y-1">
        {tabs.map(tab => {
          const Icon = icons[tab.icon]
          const active = activeTab === tab.id
          return (
            <button
              key={tab.id}
              onClick={() => {
                onTabChange(tab.id)
                onClose?.()
              }}
              className={`flex w-full items-center gap-3 rounded-xl px-4 py-3 text-left text-sm transition-all duration-200 group ${
                active
                  ? 'bg-indigo-50/80 text-indigo-600 dark:bg-indigo-950/20 dark:text-indigo-400 font-semibold shadow-sm border-l-2 border-indigo-600 dark:border-indigo-400 pl-3'
                  : 'text-slate-600 hover:bg-slate-100/80 hover:text-slate-900 dark:text-slate-400 dark:hover:bg-slate-800/60 dark:hover:text-slate-200 hover:pl-5'
              }`}
            >
              {Icon && <Icon className={`h-4 w-4 shrink-0 transition-transform duration-200 group-hover:scale-110 ${active ? 'text-indigo-600 dark:text-indigo-400' : 'text-slate-400 dark:text-slate-500'}`} />}
              <span className="truncate">{tab.label}</span>
            </button>
          )
        })}
      </nav>

      <div className="border-t border-slate-100 dark:border-slate-800/80 px-3 py-4 space-y-1">
        <button
          onClick={onToggleTheme}
          className="flex w-full items-center gap-3 rounded-xl px-4 py-2.5 text-sm text-slate-600 transition-all duration-200 hover:bg-slate-100/80 hover:text-slate-900 dark:text-slate-400 dark:hover:bg-slate-800/60 dark:hover:text-slate-200"
        >
          {dark ? <SunMedium className="h-4 w-4 text-amber-500" /> : <MoonStar className="h-4 w-4 text-indigo-500" />}
          <span>{dark ? '切到浅色' : '切到深色'}</span>
        </button>
        <button
          onClick={onLogout}
          className="flex w-full items-center gap-3 rounded-xl px-4 py-2.5 text-sm text-rose-600 transition-all duration-200 hover:bg-rose-50/80 dark:text-rose-400 dark:hover:bg-rose-950/20"
        >
          <LogOut className="h-4 w-4 text-rose-500" />
          <span>退出管理台</span>
        </button>
      </div>
    </div>
  )
}

export default function Sidebar({
  tabs,
  activeTab,
  onTabChange,
  dark,
  onToggleTheme,
  onLogout,
  mobileOpen,
  onMobileClose,
}) {
  return (
    <>
      <aside className="fixed inset-y-0 left-0 z-40 hidden w-64 border-r border-slate-200/80 dark:border-slate-800/80 bg-white/70 dark:bg-slate-900/60 backdrop-blur-xl lg:flex">
        <NavContent
          tabs={tabs}
          activeTab={activeTab}
          onTabChange={onTabChange}
          dark={dark}
          onToggleTheme={onToggleTheme}
          onLogout={onLogout}
        />
      </aside>

      {mobileOpen && (
        <div className="fixed inset-0 z-50 bg-slate-950/40 backdrop-blur-sm lg:hidden" onClick={onMobileClose}>
          <aside
            className="h-full w-72 max-w-[86vw] border-r border-slate-200/80 dark:border-slate-800/80 bg-white/95 dark:bg-slate-900/95"
            onClick={e => e.stopPropagation()}
          >
            <NavContent
              tabs={tabs}
              activeTab={activeTab}
              onTabChange={onTabChange}
              dark={dark}
              onToggleTheme={onToggleTheme}
              onLogout={onLogout}
              onClose={onMobileClose}
            />
          </aside>
        </div>
      )}
    </>
  )
}

export function SidebarToggle({ open, onClick }) {
  return (
    <button onClick={onClick} className="btn-secondary p-2 lg:hidden shadow-sm" aria-label={open ? '收起导航' : '展开导航'}>
      {open ? <PanelLeftClose className="h-4 w-4" /> : <PanelLeftOpen className="h-4 w-4" />}
    </button>
  )
}

