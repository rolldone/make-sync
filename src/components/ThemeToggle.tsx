import React, { useEffect, useState } from 'react'

const STORAGE_KEY = 'makeSync:theme'

type Theme = 'dark' | 'white'

export default function ThemeToggle() {
  const [theme, setTheme] = useState<Theme>(() => {
    try {
      const stored = localStorage.getItem(STORAGE_KEY)
      return (stored === 'dark' ? 'dark' : 'white')
    } catch {
      return 'white'
    }
  })

  useEffect(() => {
    // apply theme to document element
    const root = document.documentElement
    if (theme === 'dark') {
      root.classList.add('dark')
    } else {
      root.classList.remove('dark')
    }
    try {
      localStorage.setItem(STORAGE_KEY, theme)
    } catch {}
  }, [theme])

  const toggle = () => setTheme((t) => (t === 'dark' ? 'white' : 'dark'))

  return (
    <button
      onClick={toggle}
      className="px-2 py-1 rounded-md border bg-background text-sm"
      aria-pressed={theme === 'dark'}
      aria-label="Toggle color theme"
    >
      {theme === 'dark' ? '🌙' : '☀️'}
    </button>
  )
}
