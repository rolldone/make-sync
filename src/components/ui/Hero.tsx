import React from 'react'
// Use BASE_URL for proper link prefixes
const base = import.meta.env.BASE_URL
import { Button } from './button'
import {
  Card,
  CardHeader,
  CardTitle,
  CardDescription,
  CardContent,
  CardFooter,
} from './card'

export default function Hero() {
  return (
    <section className="mx-auto max-w-7xl px-6 py-20">
      <div className="grid items-center gap-10 md:grid-cols-2">
        <div className="max-w-2xl">
          <h1 className="text-5xl font-extrabold leading-tight mb-4">
            make-sync — keep projects, files, and teams in sync
          </h1>
          <p className="text-lg text-muted-foreground mb-8">
            Lightweight file synchronisation, automated backups, and easy scheduling —
            built for developers who want reliable workflows without the overhead.
          </p>

          <div className="flex flex-wrap gap-3">
            <a href={`${base}docs`}>
              <Button size="lg">Get started — Read the docs</Button>
            </a>
            <a href="https://github.com/rolldone/make-sync" target="_blank" rel="noreferrer">
              <Button variant="ghost" size="lg">View on GitHub</Button>
            </a>
          </div>
        </div>

        <div>
          <Card>
            <CardHeader>
              <CardTitle>Example sync preview</CardTitle>
              <CardDescription>Quick status and recent runs</CardDescription>
            </CardHeader>
            <CardContent>
              <ul className="space-y-3 text-sm">
                <li className="flex items-center justify-between">
                  <span className="text-muted-foreground">Last run</span>
                  <strong>2 minutes ago</strong>
                </li>
                <li className="flex items-center justify-between">
                  <span className="text-muted-foreground">Files synced</span>
                  <strong>1,248</strong>
                </li>
                <li className="flex items-center justify-between">
                  <span className="text-muted-foreground">Last error</span>
                  <span className="text-destructive">None</span>
                </li>
              </ul>
            </CardContent>
            <CardFooter>
              <div className="w-full flex items-center justify-between">
                <span className="text-xs text-muted-foreground">Auto-sync • daily</span>
                <Button size="sm">Manage</Button>
              </div>
            </CardFooter>
          </Card>
        </div>
      </div>
    </section>
  )
}
