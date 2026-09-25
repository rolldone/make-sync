---
title: spec exec shell fallback detection
description: Kontrak fallback antar-shell pada make-sync exec.
status: draft
created: "2026-09-25"
updated: "2026-09-25"
---
# SPEC: Deteksi Fallback Shell pada `make-sync exec`

## Latar belakang

`make-sync exec` membangun kandidat command remote menggunakan `bash -lc` dan `cmd.exe /C`. Urutan kandidat ditentukan oleh `devsync.os_target`, lalu implementasi mendeteksi shell yang tidak tersedia dari stderr untuk memutuskan apakah kandidat berikutnya perlu dicoba.

Deteksi saat ini terlalu luas: pola `(bash|sh|cmd.exe|cmd)(:|...)` cocok dengan pesan Bash seperti `bash: docker-compose exec -T app php -v: command not found`. Akibatnya kegagalan command pengguna disalahartikan sebagai Bash tidak tersedia dan make-sync mencoba kandidat Windows, menghasilkan pesan `cmd.exe: command not found` yang menyesatkan.

## Tujuan

- Jalankan kandidat shell alternatif hanya bila bukti stderr secara spesifik menunjukkan executable shell pembungkus yang sedang dicoba tidak tersedia.
- Pertahankan urutan kandidat yang diprioritaskan oleh `devsync.os_target`.
- Tampilkan kegagalan command pengguna apa adanya tanpa mencoba shell alternatif.

## Di luar cakupan

- Mengubah cara quoting atau parsing argumen `make-sync exec`.
- Mengubah mode interaktif/PTY.
- Mengubah format pesan umum SSH atau strategi pemilihan OS selain keputusan retry.

## Perilaku yang diwajibkan

1. Untuk command non-interaktif, jalankan kandidat pertama sesuai `devsync.os_target` (POSIX dahulu secara default, Windows dahulu jika target mengandung `win`).
2. Coba kandidat kedua hanya bila stderr kandidat pertama secara eksplisit menyatakan executable shell pembungkus kandidat pertama tidak ditemukan/tidak dikenali.
3. Jangan mencoba kandidat kedua untuk kegagalan command di dalam shell, termasuk `bash: <command pengguna>: command not found`, error Docker, maupun exit status non-zero biasa.
4. Jika kandidat pertama gagal dan bukan karena shell pembungkus tidak tersedia, hentikan eksekusi, kembalikan kegagalan, dan jangan menghasilkan error tambahan dari kandidat lain.
5. Jika kandidat pertama gagal karena shell pembungkus tidak tersedia, coba kandidat kedua satu kali. Jika kandidat kedua gagal, laporkan kegagalannya dan keluar non-zero tanpa retry lebih lanjut.
6. Klasifikasi berlaku untuk pola diagnostik POSIX/Windows yang didukung, termasuk variasi umum `not found`, `command not found`, dan Windows `is not recognized`, tetapi harus mengidentifikasi nama shell sebagai command yang hilang—bukan sekadar menemukan kata `bash`, `sh`, `cmd`, atau `cmd.exe` di stderr.
7. Output stdout/stderr tetap di-stream sebagaimana saat ini; retry tidak boleh menelan output diagnostik yang sudah dikeluarkan.

## Kriteria penerimaan

- Stderr `bash: docker-compose exec -T app php -v: command not found` tidak diklasifikasikan sebagai shell Bash hilang dan tidak memicu kandidat Windows.
- Stderr yang secara eksplisit menunjukkan executable wrapper `bash`/`sh` tidak tersedia diklasifikasikan sebagai shell hilang dan mengizinkan satu fallback.
- Error `cmd.exe`/`cmd` yang secara eksplisit tidak dikenali dapat mengizinkan fallback ke POSIX saat kandidat Windows dicoba lebih dahulu.
- Error command pengguna dan exit non-zero tidak memicu fallback.
- Pemilihan urutan kandidat berdasarkan `devsync.os_target` tidak berubah.
- Unit test untuk klasifikasi diagnostik lulus; seluruh test Go yang relevan lulus.

## Area implementasi yang diperkirakan

- `cmd/exec.go`: ganti matcher luas dengan classifier yang ketat pada diagnostik shell hilang.
- `cmd/exec_test.go`: uji positif/negatif untuk variasi stderr, terutama reproduksi error Docker-compose di atas.
