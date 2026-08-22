Ringkasan sesi (singkat)

Tanggal: 2025-09-28

Perubahan utama yang dibuat:

1. PTY/termio perbaikan
- `internal/devsync/termio_unix.go` dan `termio_darwin.go`
  - Menghapus penggunaan ioctl yang tidak portable (TCFLSH/FIONREAD) dan mengganti dengan pendekatan non-blocking `/dev/tty` drain untuk Unix non-darwin, dan fallback konservatif untuk darwin.

2. Windows local PTY (sebelumnya)
- Beberapa perbaikan PTY sizing, `cls` intercept, Close() behaviour sudah diterapkan (di file terkait) selama sesi.

3. SSH PTY bridge reconnect
- `internal/sshclient/ptysshbridge.go` ditambahkan handler `handleDisconnect` yang mencoba reconnect dengan exponential backoff jika pembaca stdout mendeteksi error.

4. Watcher auto-retry dan status-line
- `internal/devsync/watcher.go`:
  - `runAgentWatchCommand` sekarang memiliki retry wrapper: jika `RunCommandWithStream` gagal, watcher akan mencoba reconnect SSH client dan men-restart perintah agent dengan exponential backoff (max 6 coba).
  - Menampilkan status-line saat sedang reconnect: `w.safeStatusln("🔌 Reconnecting (attempt %d/%d) — next in %s: %v", ...)`.
  - Mengubah beberapa log error menjadi status-line untuk feedback yang lebih jelas di UI.

5. Kebijakan callback & deadlock
- Diskusi dan rekomendasi: jangan panggil callback eksternal sambil pegang mutex internal; gunakan pola copy-and-release sebelum memanggil listener.

Catatan operasional singkat:
- Untuk mengetes reconnect: jalankan watcher, matikan Wi-Fi sementara, hidupkan kembali, amati status-line "Reconnecting" lalu "Reconnected".
- Jika kamu melihat `unexpected packet in response to channel open` ketika membuat slot baru, kemungkinan karena race / session-state tidak stabil. Rekomendasi: tambahkan retry kecil saat `CreatePTYSession`.

Next steps (opsional):
- Tambah konfigurasi retry di `config` untuk `runAgentWatchCommand`.
- Tambah small retry saat pembuatan PTY/slot.
- Tambah heartbeat/ping untuk deteksi disconnect lebih agresif.

---
File ini dibuat secara otomatis oleh Copilot pada permintaan user. Jika mau saya commit perubahan ini ke git, bilang ya.
