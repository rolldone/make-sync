---
title: wo tighten exec shell fallback detection
description: Implementasi teknis classifier fallback shell sesuai SPEC.
status: complete
created: "2026-09-25"
updated: "2026-09-25"
---
# Work Order: Perketat Deteksi Fallback Shell `make-sync exec`

## Tujuan

Implementasikan kontrak pada `spec-exec-shell-fallback-detection.md`: fallback antar kandidat shell hanya terjadi bila shell pembungkus kandidat saat ini terbukti tidak tersedia. Kegagalan command yang dijalankan di dalam Bash tidak boleh memicu fallback Windows.

## Temuan teknis

- `cmd/exec.go` membangun command POSIX (`bash -lc ...`) dan Windows (`cmd.exe /C ...`), lalu memilih urutan berdasarkan `cfg.Devsync.OSTarget`.
- Regex `shellMissingRe` saat ini menerima nama shell diikuti titik dua tanpa memastikan shell tersebut yang hilang. Ini cocok dengan prefiks Bash pada diagnostik command yang tidak ditemukan.
- Loop streaming mengumpulkan stderr ke `stderrBuf`; gunakan diagnostik itu untuk klasifikasi. Jangan mengubah perilaku stream, aturan sukses, maupun urutan kandidat.

## File yang boleh diubah

1. `cmd/exec.go`
   - Hapus/replace `shellMissingRe` global-lokal dengan helper classifier bernama jelas (contoh: `isShellUnavailable(stderr string, attemptedShell shellKind) bool`) atau desain setara yang membatasi matching pada shell wrapper kandidat yang baru dijalankan.
   - Identifikasi jenis shell dari kandidat yang dieksekusi (POSIX vs Windows) secara eksplisit; hindari menginferensikan identitas shell hanya karena nama muncul di mana saja pada stderr.
   - Cocokkan hanya pola diagnostik “executable shell tidak ditemukan/tidak dikenali”, dengan variasi umum platform, tanpa mencocokkan `bash: <command pengguna>: command not found`.
   - Retry hanya bila classifier true dan masih ada kandidat berikutnya. Selain itu, pertahankan terminasi non-zero saat ini.
2. `cmd/exec_test.go`
   - Tambahkan table-driven unit test untuk classifier.
   - Kasus negatif wajib: `bash: docker-compose exec -T app php -v: command not found`, termasuk diagnostik Bash tentang command aplikasi lain yang hilang.
   - Kasus positif wajib: pola umum POSIX `bash: command not found`, `/bin/sh: 1: bash: not found`, dan pola Windows `cmd.exe is not recognized as an internal or external command` sesuai jenis kandidat.
   - Kasus negatif tambahan: error Docker biasa, exit non-zero tanpa diagnostik shell hilang, atau penyebutan shell dalam pesan yang tidak menunjukkan executable shell itu hilang.
   - Verifikasi kandidat POSIX/Windows tidak salah mengklasifikasikan diagnostik shell lain.

## Rencana implementasi

1. Ekstrak klasifikasi menjadi fungsi murni dan testable; tetapkan jenis shell wrapper yang dicoba dari kandidat, bukan dari isi stderr semata.
2. Tambahkan unit test positif dan negatif sebelum menghubungkannya ke loop retry.
3. Ganti kondisi retry di loop stream dengan classifier terhadap stderr kandidat yang saat ini gagal.
4. Pastikan kandidat alternatif hanya dicoba satu kali, hanya sesudah shell hilang terdeteksi, dan output yang telah di-stream tetap terjaga.
5. Jalankan `go test ./cmd` lalu `go test ./...`; tinjau seluruh diff untuk memastikan hanya file ruang lingkup yang berubah.

## Kriteria selesai

- Semua acceptance criteria SPEC dipenuhi.
- Regression test mereproduksi stderr dari laporan pengguna dan membuktikan fallback tidak dijalankan.
- Test classifier mencakup kondisi POSIX dan Windows positif, serta kasus negatif command-level.
- `go test ./cmd` dan `go test ./...` lulus, atau blocker test suite dilaporkan secara spesifik.
- Work Order ditandai `complete` setelah implementasi dan validasi selesai; tetap `draft` sampai pekerjaan dimulai.

## Batasan

- Jangan ubah SPEC atau file di luar topik/work order ini tanpa kebutuhan yang disetujui.
- Jangan mengubah `cmd/exec.go` selain perilaku deteksi/retry fallback yang diminta.
- Jangan melakukan push.
