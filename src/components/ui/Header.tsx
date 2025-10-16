import React from 'react'
// Use Vite’s BASE_URL to prefix all internal links
const base = import.meta.env.BASE_URL
export default function Header() {
  return (
    <header className="w-full border-b bg-background">
      <div className="mx-auto max-w-7xl px-6 py-4 flex items-center justify-between">
        <a href={`${base}`} className="font-semibold text-lg text-foreground">
          make-sync
        </a>
        <nav className="flex items-center gap-4">
          <div className="space-x-4 hidden sm:block">
            <a href={`${base}`} className="text-sm text-muted-foreground hover:underline hover:text-foreground">Home</a>
            <a href={`${base}docs`} className="text-sm text-muted-foreground hover:underline hover:text-foreground">Documentation</a>
            <a href="https://github.com/rolldone/make-sync" className="text-sm text-muted-foreground hover:underline hover:text-foreground" target="_blank" rel="noreferrer">GitHub</a>
            
          </div>
          <div>
            <div id="theme-toggle-placeholder"></div>
          </div>
        </nav>
      </div>
    </header>
  )
}
