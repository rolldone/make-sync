import React from 'react'

export default function Footer() {
  return (
    <footer className="border-t mt-12 py-8">
      <div className="mx-auto max-w-5xl px-4 text-center text-sm text-muted-foreground">
        © {new Date().getFullYear()} make-sync — Built with shadcn + Astro
      </div>
    </footer>
  )
}
