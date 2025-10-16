---
title: "Menu Interaktif"
---

# Menu Interaktif (Cara Pakai)

1. Jalankan binary:
   ```bash
   make-sync       # Unix
   make-sync.exe   # Windows
   ```
2. Pilih `DevSync` → `Single/Manual Sync`.
3. Pilih mode transfer:
   - **Download**
   - **Upload**
4. Pilih **Scope**:
   - Satu path terdaftar (dari `devsync.manual_transfer`)
   - Semua path terdaftar
   - Berdasarkan pola `!patterns` di `.sync_ignore`
5. Pilih **Mode**:
   - **Rsync Soft Mode**: hanya transfer file yang berubah, tanpa penghapusan
   - **Rsync Force Mode**: transfer + hapus file yang tidak ada di sisi lain (terbatas pada scope)

**Navigasi Keyboard TUI**:
- `Back`: naik satu level menu
- `Esc`, `q`, `Ctrl+C`: keluar dari flow Single/Manual Sync
- Force Mode tidak menampilkan prompt konfirmasi