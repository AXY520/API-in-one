import React, { useState, useEffect, useCallback, createContext, useContext } from 'react'

const ToastContext = createContext()

export function toast(message, isError = false) {
  const fn = window.__toast
  if (fn) fn(message, isError)
}

export function useToast() {
  return useContext(ToastContext)
}

let toastId = 0

export default function Toast() {
  const [items, setItems] = useState([])

  const addToast = useCallback((message, isError = false) => {
    const id = ++toastId
    setItems(prev => [...prev, { id, message, isError, leaving: false }])
    setTimeout(() => {
      setItems(prev => prev.map(t => t.id === id ? { ...t, leaving: true } : t))
      setTimeout(() => {
        setItems(prev => prev.filter(t => t.id !== id))
      }, 300)
    }, 3500)
  }, [])

  useEffect(() => {
    window.__toast = addToast
    return () => { window.__toast = undefined }
  }, [addToast])

  return (
    <div className="toast-container">
      {items.map(item => (
        <div
          key={item.id}
          className={`toast ${item.isError ? 'toast-error' : 'toast-success'} ${item.leaving ? 'opacity-0 translate-x-12 scale-95' : 'opacity-100'}`}
          style={{ transition: 'all 0.3s cubic-bezier(0.34, 1.56, 0.64, 1)' }}
        >
          <svg className="w-5 h-5 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2.2}>
            {item.isError
              ? <path strokeLinecap="round" strokeLinejoin="round" d="M10 14l2-2m0 0l2-2m-2 2l-2-2m2 2l2 2m7-2a9 9 0 11-18 0 9 9 0 0118 0z" />
              : <path strokeLinecap="round" strokeLinejoin="round" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z" />
            }
          </svg>
          <span className="font-medium tracking-wide">{item.message}</span>
        </div>
      ))}
    </div>
  )
}

