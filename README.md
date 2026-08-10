# make-sync

CLI untuk sinkronisasi file/devsync via SSH, dengan mode aman (safe pull/push), menu interaktif, dan dukungan manual sync berbasis path terdaftar serta ignore per-item di `devsync.manual_transfer`.

PERINGATAN: Fitur "Pipeline System" telah dihapus dari rilis saat ini. Bagian pipeline di README ini dipertahankan sebagai arsip/dokumentasi lama saja — fitur tidak lagi didukung. Untuk migrasi atau akses arsip, lihat dokumentasi internal atau hubungi tim.

## Fitur Utama
- Safe Pull/Push: indeks remote (SQLite) dibuat dulu, lalu bandingkan hash untuk unduh/unggah yang berubah saja.
- One-Shot Push/Pull (CLI): perintah `make-sync push` / `make-sync pull` untuk sinkronisasi sekali jalan — mendukung `--path` (scoped), `--force`, dan `--bypass-ignore`. (`--git-changes` sementara dinonaktifkan.)
- Manual/Single Sync Menu:
  - Download/Upload
  - Pilih folder: salah satu path terdaftar atau semua yang terdaftar
  - Ignore per folder ditentukan di `devsync.manual_transfer` (string = path saja, object = path + ignores)
  - Mode: Soft (tanpa delete) atau Force (dengan delete terbatas scope)
  - Catatan: opsi "Bypass" telah dihapus dari menu top-level Pull/Push. Opsi bypass-ignore masih tersedia sebagai flag CLI/agent untuk pengguna lanjutan, tetapi tidak ditampilkan pada menu interaktif utama untuk mencegah penghapusan tak sengaja.
- Force Mode (Rsync-like delete):
  - Download Force: hapus file lokal yang tidak ada di DB remote dalam scope pilihan
  - Upload Force: hapus file remote yang tidak ada di lokal (gunakan kolom `checked` di DB bila ada)
  - Global `.sync_ignore` cerdas: menghormati `.sync_temp` dan pola wildcard (termasuk `**`) untuk flow global; Single/Manual Sync memakai ignore per-item di `devsync.manual_transfer`.
- SSH Agent Remote: otomatis build atau pakai fallback binary, jalankan indexing di remote, unduh DB.

## Instalasi & Build
- Butuh Go 1.24+.
- Jalankan:

```
go build ./...
```

Jika ingin menjalankan seluruh test (yang ada):

```bash
go test ./...
```

## Konfigurasi
Konfigurasi utama di `make-sync.yaml`. Contoh minimal:

```yaml
local_path: .
remote_path: /home/user/project

devsync:
  os_target: linux  # atau windows
  auth:
    host: 1.2.3.4
    port: "22"
    username: youruser
    private_key: C:\\Users\\you\\.ssh\\id_rsa
    remote_path: /home/user/project
    local_path: .
  ignores:
    - .sync_temp
  manual_transfer:
    - src
    - path: assets/images
      ignores:
        - "*"
        - "!test/kokok.txt"
```

Catatan:
- `devsync.auth.remote_path` adalah root project di remote.
- `devsync.manual_transfer` bisa berisi string path sederhana atau object `{ path, ignores }`.
- `manual_transfer` object memakai `ignores` lokal ke path tersebut; pattern negasi `!pattern` sebaiknya di-quote di YAML, misalnya `"!test/kokok.txt"`.
- `.sync_ignore` di root tetap dipakai oleh flow global, tetapi Manual/Single Sync memakai ignore dari `devsync.manual_transfer`.

## Konfigurasi Direct Access (SSH Config)

make-sync mendukung pembuatan file SSH config otomatis untuk akses langsung ke remote server. Konfigurasi ini memungkinkan koneksi SSH yang lebih mudah tanpa perlu mengetik kredensial berulang.

### RemoteCommand Berdasarkan Target OS

`RemoteCommand` di `direct_access.ssh_configs` harus disesuaikan dengan target OS:

#### Untuk Windows Target:
```yaml
RemoteCommand: cmd /K cd =remotePath
```
- Menggunakan `cmd /K` untuk menjaga command prompt tetap terbuka
- `cd =remotePath` untuk berpindah ke direktori project

#### Untuk Linux/Unix Target:
```yaml
RemoteCommand: cd =remotePath && bash -l
```
- Menggunakan `bash -l` untuk login shell
- `cd =remotePath` untuk berpindah ke direktori project

### Cara Kerja:
1. Jalankan `make-sync direct_access` untuk generate SSH config
2. File config akan dibuat di `.sync_temp/.ssh/config`
3. Gunakan `ssh my-server` untuk koneksi langsung
4. RemoteCommand akan otomatis menjalankan command sesuai target OS

## Cara Pakai (Menu Interaktif)
1. Jalankan binary `make-sync.exe` (Windows) atau `make-sync` (Unix).
2. Di menu utama pilih DevSync → Single/Manual Sync.
3. Pilih Download atau Upload.
4. Pilih scope:
   - Salah satu path terdaftar (dari `devsync.manual_transfer`)
   - "All data registered in manual_sync only" (semua path terdaftar)
  - Jika entry `manual_transfer` bertipe object, ignore mengikuti rules di entry itu
5. Pilih mode:
  - Soft Mode: hanya transfer file yang berubah, tanpa penghapusan
  - Force Mode: selain transfer, juga melakukan penghapusan file yang tidak ada pada sisi lain, terbatas pada scope pilihan, dan tetap menghormati ignore per-item `manual_transfer`
  - Catatan: Mode Force sekarang dilindungi dengan konfirmasi captcha untuk operasi destruktif. Ini mencegah eksekusi Force oleh kelalaian. Jika Anda perlu menggunakan bypass-ignore, gunakan flag CLI/agent dengan kehati-hatian.

Selama Download/Upload:
- Sistem akan menjalankan indexing di remote terlebih dahulu (mirroring safe pull/push) agar DB up-to-date.

### Navigasi Keyboard di TUI
- Back bertahap: gunakan item menu "Back" untuk naik satu level.
- Keluar cepat: Esc, q, atau Ctrl+C akan keluar dari seluruh flow Single/Manual Sync.
- Catatan: Mode Force sekarang dilindungi konfirmasi captcha untuk operasi destruktif; opsi bypass dihilangkan dari menu top-level. Jika Anda memerlukan bypass, gunakan flag CLI/agent secara eksplisit (akan meminta kehati-hatian).

## Detail Perilaku
- Download Soft: unduh file remote yang belum ada atau hash berbeda, dalam scope.
- Download Force: setelah unduh, hapus file lokal yang match scope namun tidak ada di DB remote.
- Upload Soft: unggah file lokal yang belum ada atau hash berbeda, dalam scope.
- Upload Force: tandai setiap file lokal yang diproses sebagai `checked` (di DB bila tersedia), lalu hapus file remote match scope yang tidak `checked`.
- Scope `manual_transfer` ignores:
  - Entry string berarti path saja, tanpa ignore tambahan.
  - Entry object mendukung `ignores` lokal ke path tersebut.
  - `ignores` mendukung wildcard (`*`, `**`) dan negation `!pattern`.
  - `.sync_temp` selalu diabaikan.
  - Untuk include satu file di subtree yang di-ignore, pakai pattern negasi yang di-quote di YAML, misalnya `"!test/kokok.txt"`.

## Tips & Batasan
- Pastikan `devsync.auth` terisi benar untuk koneksi SSH.
- Jika build agent gagal, sistem akan mencoba fallback binary di folder `.sync_temp` sesuai target OS.
- Mode Force sekarang menggunakan konfirmasi captcha untuk operasi destruktif; opsi bypass dihilangkan dari menu top-level. Jika Anda memerlukan bypass, gunakan flag CLI/agent secara eksplisit (akan meminta kehati-hatian).
- Untuk Windows remote, penghapusan file menggunakan `cmd.exe /C del /f /q`.

### Catatan tentang `.sync_temp` dan ignore
- `.sync_temp` selalu dikecualikan dari sinkronisasi.
- `.sync_ignore` tetap dipakai oleh flow global, tetapi Manual/Single Sync mengikuti ignore rules per-item di `devsync.manual_transfer`.

## One-Shot Push / Pull (CLI)

Selain menu interaktif, tersedia perintah one-shot `push` dan `pull` untuk sinkronisasi sekali jalan dari terminal:

```bash
make-sync push                  # Soft push — unggah file yang berubah saja (aman, tanpa delete)
make-sync push --force          # Force push — unggah + hapus file remote yang tidak ada di lokal
make-sync push --path <path>    # Scoped push — hanya path tertentu (bisa diulang)
make-sync push --bypass-ignore  # Lewati filter .sync_ignore (termasuk file yang di-ignore)
```

```bash
make-sync pull                  # Soft pull — unduh file yang berubah saja (aman, tanpa delete)
make-sync pull --force          # Force pull — unduh + hapus file lokal yang tidak ada di remote
make-sync pull --path <path>    # Scoped pull — hanya path tertentu (bisa diulang)
make-sync pull --bypass-ignore  # Lewati filter .sync_ignore (termasuk file yang di-ignore)
```

Kombinasi flag:

```bash
make-sync push --force --path src   # Force push hanya dalam scope src/
make-sync pull --path data --bypass-ignore  # Pull scoped + bypass
```

Catatan perilaku:
- `--git-changes` sementara dinonaktifkan — jika dipakai, perintah ditolak dengan pesan error. Gunakan `--path` untuk scope.
- `--bypass-ignore` menonaktifkan seluruh filter ignore — hati-hati karena file sensitif (mis. `.env`, `.ssh_encrypto`, `.sync_collections/**`) ikut ditransfer.
- `--force` selalu dilindungi konfirmasi captcha untuk mencegah penghapusan tidak sengaja.
- Tanpa flag scope (`--path`), perintah berjalan sebagai full-project sync memakai mode Safe/Bypass/Force dari mesin pull/push.

## Eksekusi Perintah Remote (exec)

Jalankan perintah langsung di host remote. Output di-stream real-time; proses akan mengembalikan exit status remote.

Penggunaan:

```bash
make-sync exec <command...>
```

### Cara Kerja (POSIX / Linux / Unix)

Semua token setelah `exec` digabung menjadi satu command string lalu diteruskan **secara mentah** ke remote. Di remote, command dijalankan oleh `bash -lc` (bukan lagi `base64`/`bash -s`), sehingga kutipan (tunggal/ganda/tanpa kutip), variabel, dan operator (`&&`, `|`, `>`, dsb.) diinterpretasikan secara natural — persis seperti Anda mengetik langsung di terminal server.

Jika `devsync.auth.remote_path` (fallback `remote_path`) diisi, command dibungkus dengan prefix `cd <remotePath> &&` sehingga Anda otomatis bekerja dari direktori kerja remote.

Untuk remote Windows, client mencoba fallback `cmd.exe /C`.

### Mode Interaktif (PTY)

`exec` mendeteksi kebutuhan sesi interaktif secara otomatis:

- Perintah yang mengandung `-it` / `-ti` / `-t` (mis. `docker compose exec -it app bash`) atau diakhiri `bash`/`sh`/`zsh` tanpa `-c` dianggap interaktif.
- Anda juga bisa memaksa mode interaktif dengan flag `-it` / `--it` / `-i` / `--interactive` setelah `exec`, mis. `make-sync exec -it <cmd>`.

Saat mode interaktif aktif, sesi PTY akan dibuka di remote. Ketik `exit` untuk keluar; jika masuk ke container (mis. via `docker compose exec`), `exit` akan kembali ke shell host sebelum sesi berakhir.

> **Catatan:** Mode interaktif membutuhkan TTY nyata di lokal. Jika stdin Anda di-pipe (bukan terminal), sesi interaktif tidak dapat dibuka — gunakan perintah non-interactive.

### Contoh Penggunaan

- Tampilkan file tersembunyi (Linux/Unix):
  - `make-sync exec ls -a -l`
- Docker compose restart (tanpa escaping tambahan):
  - `make-sync exec docker compose down && docker compose up`
- Kutipan dan variabel bekerja natural:
  - `make-sync exec echo 'halo dunia'`
  - `make-sync exec echo $HOME`
- Masuk ke shell interaktif di dalam container:
  - `make-sync exec docker compose exec -it app bash`

### Keterbatasan & Prasyarat

- POSIX: remote harus menyediakan `bash`/`sh`. **Tidak lagi membutuhkan `base64`.**
- Windows: remote harus menyediakan `cmd.exe`.
- Tidak ada parsing flag lokal untuk `exec`; semua token setelah `exec` dianggap bagian dari perintah remote.

### Troubleshooting

- "command not found": pastikan perintah tersedia di PATH remote, atau gunakan path absolut/bin yang tepat.
- Salah direktori kerja: set `devsync.auth.remote_path` (atau `remote_path`) pada config agar `cd` otomatis ke root project di remote.

## Troubleshooting
- Tidak ada file yang tertransfer:
  - Cek apakah hash sama atau scope/pola tidak match.
- Gagal indexing remote:
  - Periksa kredensial SSH, jalur `remote_path`, dan izin eksekusi pada binary agent (Unix: chmod +x).
- Pola `!pattern` pada `manual_transfer` object tidak bekerja:
  - Pastikan `ignores` ditulis sebagai array string dan pattern negasi di-quote jika diawali `!`, misalnya `"!test/kokok.txt"`.

## Lisensi
Proyek ini mengikuti lisensi yang ditentukan oleh pemilik repositori.
