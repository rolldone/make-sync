import React from 'react'

export default function Header() {
  return (
    <header style={{padding: '12px 16px', borderBottom: '1px solid #eee', display: 'flex', alignItems: 'center', justifyContent: 'space-between'}}>
      <div style={{fontWeight: 700}}>make-sync</div>
      <nav>
        <a href="/" style={{marginRight: 12}}>Home</a>
        <a href="/docs" style={{marginRight: 12}}>Documentation</a>
        <a href="https://github.com/" target="_blank" rel="noopener" style={{marginRight:12}}>GitHub</a>
        
      </nav>
    </header>
  )
}
