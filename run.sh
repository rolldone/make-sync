#!/usr/bin/env bash
# run.sh - kills any existing Astro dev process and starts a new one

# Kill by process name (astro dev)
pkill -f "astro dev" || true

# Alternatively, kill process listening on port 3000
if lsof -ti:3000 >/dev/null; then
  lsof -ti:3000 | xargs kill -9 || true
fi

# Start Astro dev server
npm run dev