---
title: "Tips & Troubleshooting"
---

## Tips & Troubleshooting

- Pastikan `devsync.auth` benar untuk koneksi SSH.
- Build agent fallback ke binary di `.sync_temp` jika gagal.
- Force Mode tidak menampilkan konfirmasi: cek scope sebelum menjalankan.
- Untuk Windows remote, penghapusan pakai `cmd.exe /C del /f /q`.

### Troubleshooting

- “command not found”: pastikan perintah ada di PATH remote atau gunakan path absolut.
- “Salah direktori kerja”: atur `devsync.auth.remote_path` atau `remote_path` dengan benar.
- Pola `!pattern` tidak bekerja: cek `.sync_ignore` di root, tanpa komentar, satu pola per baris.
