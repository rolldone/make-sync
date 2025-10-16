---
title: "Memulai dengan make-sync"
order: 20
---

# Memulai dengan make-sync

make-sync adalah CLI untuk menyinkronkan file antara lokal dan remote dengan mode safe pull/push.

## Instalasi

Butuh Go 1.24+. Untuk membangun:

```bash
go build ./...
```

## Contoh konfigurasi

Contoh `make-sync.yaml` minimal:

```yaml
local_path: .
remote_path: /home/user/project


## Inisialisasi proyek (make-sync init)

Jika repository berisi `make-sync-sample.yaml` di root proyek, jalankan perintah berikut untuk membuat konfigurasi awal tanpa menimpa file yang sudah ada:

```bash
make-sync init
```

Apa yang dilakukan perintah ini:

- Mencari `make-sync-sample.yaml` di root proyek (fungsi util mencoba menemukan root menggunakan `go.mod`, `main.go`, atau `make-sync.yaml`).
- Jika ditemukan, `init` akan membuat dua file baru dengan nama unik:
  - `make-sync-<sufiks>.yaml` — salinan konfigurasi (unik agar tidak menimpa file lain)
  - `.sync_ignore_<sufiks>` — file ignore yang berisi pola default (git, node_modules, IDE files, dsb.)
- Perintah menampilkan instruksi untuk menyalin file hasil ke nama yang diharapkan:

```bash
cp make-sync-<sufiks>.yaml make-sync.yaml
cp .sync_ignore_<sufiks> .sync_ignore
```

Langkah yang disarankan setelah `init`:

1. Salin file hasil menjadi file yang digunakan oleh CLI, lalu edit `make-sync.yaml` untuk menyesuaikan host, key path, dan variabel sensitif:

```bash
devsync:
  os_target: linux
  auth:
    host: 1.2.3.4
    port: "22"

2. Periksa dan hapus placeholder/secret contoh (contoh: `password: secret123`) dan pastikan `privateKey` menunjuk ke path kunci SSH yang benar.

3. Jalankan `make-sync` untuk memverifikasi konfigurasi (perintah utama akan memvalidasi dan menampilkan menu jika konfigurasi valid):

```bash
    username: youruser
    private_key: /home/you/.ssh/id_rsa
    remote_path: /home/user/project
    local_path: .

Catatan keamanan & tips:

- `make-sync-sample.yaml` dalam repo mungkin berisi contoh credential. Jangan gunakan credential contoh di lingkungan produksi.
- `init` sengaja tidak menimpa file `make-sync.yaml`; ini memberi kesempatan untuk meninjau konfigurasi sebelum dijadikan aktif.
- Jalankan `make-sync init` dari root proyek agar sample ditemukan oleh fungsi pencarian project root.

  ignores:
    - .sync_temp
  manual_transfer:
    - src
    - assets/images
```
