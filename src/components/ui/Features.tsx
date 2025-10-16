import React from 'react'
import { Card, CardHeader, CardTitle, CardContent } from './card'
import { Badge } from './badge'

export default function Features() {
  const items = [
    { title: 'Two-way sync', desc: 'Keep folders mirrored across machines.' },
    { title: 'Incremental backups', desc: 'Only changed files are transferred.' },
    { title: 'Easy config', desc: 'Simple YAML config to define sync rules.' },
  ]

  return (
    <section className="mx-auto max-w-5xl px-4 py-10">
      <div className="grid gap-6 sm:grid-cols-3">
        {items.map((it) => (
          <Card key={it.title} className="p-4">
            <CardHeader>
              <Badge>{it.title}</Badge>
              <CardTitle className="mt-2 text-lg">{it.title}</CardTitle>
            </CardHeader>
            <CardContent>
              <p className="text-sm text-muted-foreground">{it.desc}</p>
            </CardContent>
          </Card>
        ))}
      </div>
    </section>
  )
}
