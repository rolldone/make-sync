---
title: "Fitur Utama"
---

## Fitur Utama

- Safe Pull/Push: indeks remote (SQLite) dibuat dulu, lalu bandingkan hash untuk unduh/unggah yang berubah saja.
- Manual/Single Sync Menu:
  - Download/Upload
  - Pilih folder: salah satu path terdaftar, semua yang terdaftar, atau berdasarkan pola `!` di `.sync_ignore`
  - Mode: Rsync Soft (tanpa delete) atau Rsync Force (dengan delete terbatas scope)
- Force Mode (Rsync-like delete):
  - Download Force: hapus file lokal yang tidak ada di DB remote dalam scope pilihan
  - Upload Force: hapus file remote yang tidak ada di lokal (gunakan kolom `checked` di DB bila ada)
- `.sync_ignore` cerdas: menghormati `.sync_temp` dan pola wildcard (termasuk `**`), serta mendukung "include by negation" via `!pattern`.
- SSH Agent Remote: otomatis build atau pakai fallback binary, jalankan indexing di remote, unduh DB.
