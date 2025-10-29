Agent ignore precedence
=======================

This agent uses a deterministic ignore strategy when performing indexing.

  it contains a `devsync.ignores` array, the agent will treat that list as
  authoritative. In that case the agent will NOT scan per-directory
  `.sync_ignore` files on disk and will apply only the uploaded patterns.

  per-directory `.sync_ignore` files and applying cascading semantics.

This design ensures the client can centrally control which files the agent
indexes (useful to avoid pulling agent artifacts or other generated files).

---

# Dokumentasi Agent (ringkasan Bahasa Indonesia)

Dokumentasi singkat untuk binary agent yang dijalankan di remote (.sync_temp).

Tujuan
- Agent adalah binary ringan yang diupload oleh controller lokal dan dijalankan di remote untuk melakukan tugas seperti:
  - Membuat index file remote (sqlite DB) untuk operasi safe pull/push
  - Menjalankan file-watcher (mode `watch`)
  - Menampilkan konfigurasi lokal (mode `config`)

Perintah dan flag penting
- indexing
  - Perintah satu kali: `sync-agent indexing`
  - Agent akan membaca `.sync_temp/config.json` di working dir (atau di direktori executable jika berada di `.sync_temp`) dan `chdir` ke `devsync.working_dir` sebelum indexing.
  - Output: `.sync_temp/indexing_files.db` (SQLite) yang kemudian diunduh controller.

- --manual-transfer
  - Bentuk 1: `sync-agent indexing --manual-transfer` (tanpa nilai)
    - Agent akan membaca `devsync.manual_transfer` dari `.sync_temp/config.json` dan hanya mengindeks prefix yang tercantum di sana.
  - Bentuk 2: `sync-agent indexing --manual-transfer prefix1,prefix2`
    - Controller bisa mengirim daftar prefix (dipisah koma) agar agent mengindeks hanya prefix tersebut.
  - Jika flag diberikan tetapi tidak ada prefix yang ditemukan (baik flag kosong dan config kosong), agent akan memperingatkan dan fallback melakukan full indexing.

- --bypass-ignore
  - Jika dipasang, agent akan mengabaikan aturan `.sync_ignore` saat indexing.

- prune (baru)
  - Perintah: `sync-agent prune`
  - Tujuan: membersihkan direktori kosong yang tersisa setelah operasi Force (atau ketika controller meminta prune). Agent melakukan traversal bottom-up dan hanya menghapus direktori yang benar-benar kosong.
  - Flags yang didukung:
    - `--manual-transfer prefix1,prefix2` — batasi prune ke subtree yang relevan (prefix relatif terhadap working_dir).
    - `--dry-run` — jangan hapus, hanya tampilkan apa yang akan dihapus.
    - `--bypass-ignore` — (opsional) menginstruksikan agent untuk mengabaikan `.sync_ignore` bila diinginkan; namun implementasi prune bersifat konservatif secara default dan akan selalu mengecualikan `.sync_temp` dan `.git`.
  - Output:
    - Agent mencetak satu baris JSON kompak pertama yang berisi ringkasan hasil, contoh:
      `{"removed":["path1","path2"],"failed":[{"path":"p","error":"..."}],"dry_run":false}`
    - Setelah JSON, agent menampilkan ringkasan manusiawi (list path yang dihapus dan kegagalan).
  - Perilaku penting:
    - Agent TIDAK akan memproses atau menghapus apa pun di bawah `.sync_temp` atau `.git` — direktori ini selalu dikecualikan.
    - Agent menghapus hanya direktori yang benar-benar kosong (konservatif) — jika sebuah direktori berisi file (termasuk file yang dicocokkan oleh `.sync_ignore`), direktori tersebut tidak akan dihapus.
    - Jika direktori hilang antara pengecekan dan penghapusan (race), ENOENT diabaikan dan tidak dilaporkan sebagai kegagalan.
    - Gunakan `--dry-run` terlebih dahulu untuk melihat apa yang akan dihapus sebelum menjalankan prune secara nyata.
  - Rekomendasi penggunaan:
    - Controller/client sekarang mem-delegasikan remote-prune ke agent alih-alih menjalankan shell `find/rmdir` via SSH. Ini meningkatkan portabilitas dan mengurangi risiko quoting/shell pada remote.
    - Jangan gunakan `--bypass-ignore` kecuali Anda paham risikonya — opsi ini dapat menyebabkan penghapusan artefak yang biasanya diabaikan.

  - Contoh penggunaan:

    ```bash
    # Lihat apa yang akan dihapus di subtree src dan assets (dry-run)
    sync-agent prune --manual-transfer src,assets --dry-run

    # Jalankan prune pada seluruh working_dir (konservatif: hanya hapus direktori benar-benar kosong)
    sync-agent prune

    # (Tidak disarankan) Jalankan prune pada satu prefix dan bypass ignore
    sync-agent prune --manual-transfer logs --bypass-ignore
    ```

    Catatan: saat controller menjalankan agent prune, controller biasanya akan membaca baris JSON pertama dari stdout untuk mengambil jumlah `removed`/`failed` secara andal.

Lokasi config & working dir
- Agent mencari `.sync_temp/config.json` di:
  1. Direktori tempat executable berada (jika executable berada di `.sync_temp`, ia akan baca di situ)
  2. `./.sync_temp/config.json` di working directory
- Field penting di config: `devsync.working_dir` (wajib untuk indexing), `devsync.manual_transfer` (opsional)

Behaviour terkait ignore dan manual_transfer
- Agent mempunyai mekanisme `SimpleIgnoreCache` yang membaca `.sync_temp/config.json` dan `.sync_ignore` di tree.
- Jika path termasuk `manual_transfer`, path tersebut diperlakukan sebagai "explicit endpoint" dan tidak diblokir oleh pola ignore (kecuali `--bypass-ignore` dipakai).

Dukungan OS
- Agent di-build per-target OS (binary `.exe` untuk Windows, tanpa ekstensi untuk Unix).
- Controller/deploy logic menyesuaikan path separator, quoting, chmod, dan cara menjalankan perintah (cmd.exe vs shell) saat mengeksekusi agent di remote.

Verifikasi cepat (non-kode)
1. Pastikan `.sync_temp/config.json` yang diupload controller memiliki `devsync.working_dir` benar.
2. Untuk percobaan: jalankan `sync-agent indexing --manual-transfer prefix1` pada remote (atau controller menjalankan perintah ini) dan lihat output yang dicetak agent.
3. Periksa file `.sync_temp/indexing_files.db` di remote (atau unduh) dan buka tabel `files` untuk memastikan entri yang diindeks sesuai prefix.
4. Jika ingin menonaktifkan ignore lokal saat testing, gunakan `--bypass-ignore`.

Catatan
- Agent tidak melakukan transfer file langsung untuk operasi sync — ia hanya membuat index (DB) dan menyediakan hashing untuk operasi download/upload yang dikontrol oleh controller.
- Pastikan versi agent dan schema DB saling kompatibel antara controller dan agent untuk menghindari masalah parsing DB.

---
