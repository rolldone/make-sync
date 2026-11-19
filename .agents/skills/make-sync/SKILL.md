---
name: make-sync
description: "Use when operating the make-sync CLI for SSH file sync."
version: 1.0.0
author: donny
license: MIT
metadata:
  hermes:
    tags: [make-sync, ssh, rsync, devsync, cli, golang]
    related_skills: [historic]
---

# make-sync — Skill Operasi CLI

## When to Use

Pakai skill ini bila tugasnya menyentuh `make-sync`: menjalankan pull/push/exec terhadap remote, mengedit atau membuat `make-sync.yaml` / `.sync_ignore`, memakai menu devsync/single-sync/direct access, atau memperbaiki repo `~/workspaces/make-sync`.

Gunakan skill ini setiap kali bekerja dengan binary `make-sync` (sinkronisasi file lokal↔remote via SSH). Binary sudah terpasang global di `/usr/local/bin/make-sync` (symlink ke build di repo `~/workspaces/make-sync`).

## Aturan workspace

- Config dibaca dari `make-sync.yaml` **di current working directory**. Selalu `cd` ke root project dulu; tidak ada flag `--config`.
- Tanpa `make-sync.yaml`, perintah apa pun gagal dengan `make-sync.yaml not found. Please run 'make-sync init' first`. Pengecualian: `make-sync data` dan `make-sync path-info` tetap jalan.
- Semua state runtime ditulis ke `.sync_temp/` (log `watcher.log`, `config.json` agent lokal, `config/.ssh/config` hasil `direct_access`, binary agent). Jangan pernah commit atau sinkronkan folder ini; ia selalu di-ignore otomatis.
- Daftar workspace terakhir disimpan di `~/.make-sync/history.json`; ini yang muncul saat `make-sync` dijalankan tanpa config.
- Binary agent (`sync-agent-<epoch>`) di-generate ke root project saat devsync/agent dipakai. Boleh dihapus, akan dibuat ulang atau fallback ke binary lama.

## Peta perintah

```
make-sync                     # menu direct access (dari direct_access.ssh_commands)
make-sync devsync             # menu DevSync: safe_sync, Pull, Push, single/manual sync, remote session
make-sync pull  [flags]       # one-shot download
make-sync push  [flags]       # one-shot upload
make-sync deploy --since-commit <rev>  # deploy file yang tersentuh rentang commit (blind push)
make-sync exec  <cmd...>      # eksekusi perintah di remote
make-sync data                # kelola koleksi config tersimpan (.sync_collections)
make-sync init                # generate config dari make-sync-sample.yaml
make-sync path-info           # debug path resolution (cwd, executable, agent, config)
```

Flag `pull`/`push` (sama untuk keduanya):

| Flag | Efek |
|---|---|
| *(tanpa flag)* | Soft sync — hanya file baru/berubah, **tanpa delete** |
| `--path <p>` | Batasi scope (repeatable) |
| `--force` | Tambah hapus file di sisi tujuan yang tidak ada di sumber (dalam scope) |
| `--bypass-ignore` | Abaikan seluruh filter `.sync_ignore` |
| `--git-changes` | **Dinonaktifkan** — selalu ditolak dengan error |

## Konfigurasi

Kunci utama `make-sync.yaml`: `var:`, `devsync:`, `direct_access:`, `sync_collection:`, `project_name:`, `reset_cache:`.

- `var:` — map nilai yang bisa dirujuk sebagai `=var.auth.host`, `=var.credentials.password`, dst. Rujukan diselesaikan iteratif (transitive) dan dideteksi cycle. Nilai `=...` **hanya** di-render di field yang memang di-render (auth, ssh_configs, ssh_commands); jangan mengandalkannya di tempat lain.
- `devsync.auth` — `host`, `port`, `username`, `privateKey`, `remotePath`. Ini sumber koneksi untuk pull/push/exec/agent.
- `devsync.manual_transfer` — daftar path yang boleh dipilih di menu single sync. Entri boleh string (`src`) atau object (`{path: ..., ignores: [...]}`). Ignore object memakai pola wildcard dan negasi `!pattern` (quote di YAML: `"!test/kokok.txt"`).
- `devsync.ignores` — ignore untuk flow global; `.sync_ignore` di root tetap dipakai flow global, sedangkan single/manual sync memakai ignore per-item di `manual_transfer`.
- `devsync.concurrency` — worker paralel SFTP; `size_limit` (MB, 0 = tanpa batas); `os_target` (`linux`/`windows`) mempengaruhi sintaks `RemoteCommand` dan cara delete remote.
- `devsync.script` — daftar command lokal/remote yang ditawarkan di menu; ini string shell, dijalankan apa adanya, bukan template.
- `direct_access.ssh_configs` — entri SSH config; `RemoteCommand` untuk Linux: `cd =remotePath && bash -l`, untuk Windows: `cmd /K cd =remotePath`. `direct_access.ssh_commands` — item menu utama, boleh berisi `command` atau `port_forwards` (tunnel).
- `sync_collection.src` (default `.sync_collections`) + `files` — file yang dibundel saat `make-sync data save`.

`.sync_ignore` menerima dua format: YAML list (`- node_modules`) **atau** gaya `.gitignore` baris-per-baris. `make-sync init` membuat `make-sync-<suffix>.yaml` + `.sync_ignore_<suffix>` dengan suffix unik agar tidak menimpa file yang ada.

## Mode sync dan penghapusan

- **Soft** = aman, tidak pernah menghapus. Ini default; jadikan pilihan pertama.
- **Force** = download force menghapus file lokal yang tidak ada di DB remote; upload force menghapus file remote yang tidak ada di lokal. Selalu terbatas scope dan tetap menghormati ignore.
- Force di flow interaktif dilindungi captcha (`tui.ConfirmWithCaptcha`). Untuk otomasi tanpa captcha, set `MAKE_SYNC_FORCE_CAPTCHA=false` — perlakukan sebagai tindakan destruktif: konfirmasi ke pengguna lebih dulu.
- Force memakai perbandingan hash dari index SQLite remote yang dibangun lebih dulu, bukan `rsync --delete`. Karena itu jangan menganggap file yang “hilang” di satu sisi otomatis akan dihapus di luar scope yang dipilih.
- `--force --path <x>` adalah bentuk force paling aman untuk dipakai otomatis: scope sempit dan eksplisit.

## deploy — delta push by commit range (dua mode)

`make-sync deploy` mengirim ke remote hanya file yang tersentuh rentang/commit git, dengan delta pre-check `sha256sum` (satu exec SSH, tanpa agent/DB). Dua mode **saling eksklusif**:

- `--since-commit <rev>` — **maju**. Kandidat = file berubah pada `<rev>..HEAD` ditambah perubahan working tree (modified/added/untracked). File yang dihapus dalam rentang dihapus di remote.
- `--to-commit <rev>` — **mundur (revert remote)**. Perbandingan murni git: `tree <rev>` vs `tree HEAD`; working tree diabaikan (warning 1 baris bila ada uncommitted). Aksi:
  - isi berbeda → unggah isi versi `<rev>`
  - ada di HEAD, hilang di `<rev>` → hapus di remote
  - ada di `<rev>`, hilang di HEAD → unggah kembali
  - tidak ada di keduanya / file manual server → tidak disentuh

- **Tanpa indexing/agent.** Tidak ada build agent, upload config, indexing remote, atau `indexing_files.db` — jauh lebih cepat daripada `push`. Git adalah sumber kebenaran.
- **Delta push.** Satu exec SSH `sha256sum` untuk semua path remote; file identik di-skip (`SKIP x / SEND y`). Untuk `--to-commit`, hash lokal berasal dari **blob git versi `<rev>`**, bukan file disk; blob di-materialize ke direktori sementara lalu dibersihkan.
- **Idempotent.** Jalankan ulang perintah yang sama → semua SKIP + `✅ up-to-date`. Balik arah dengan `--since-commit <rev>` yang sama untuk kembali ke HEAD.
- **Fallback.** `sha256sum` tidak ada / exec gagal / output tak ter-parse → warning + blind push semua file (tidak gagal keras). Target Windows selalu blind upload (sha256sum bukan standar cmd.exe).
- **Deletion scoped + captcha.** Deletion per-path (`rm -f`, batch 100) — **bukan** sweep seperti `--force`. Daftar dicetak dulu lalu wajib captcha (`MAKE_SYNC_FORCE_CAPTCHA=false` berlaku). Ditolak → upload tetap jalan, deletion dilewati.
- **Rename** = hapus path lama + unggah path baru (semantik git).
- `<rev>` menerima hash (full/short), `HEAD`, `HEAD~n`, nama branch, dan tag.
- Filter `.sync_ignore` sisi lokal tetap berlaku di kedua mode.
- Untracked besar (`vendor/`, `docs/`) ikut terkirim di mode `--since-commit` — pastikan `.sync_ignore` memfilternya. Mode `--to-commit` tidak terpengaruh karena hanya memakai tree commit.
- Perbandingan dengan `push`: `push` = sinkronisasi berbasis index/hash (bandingkan remote); `deploy` = kirim state git ke remote (tanpa indexing). `push`, `pull`, `exec` tidak berubah perilaku.

## exec — quoting

`make-sync exec` meneruskan token apa adanya ke shell remote (`bash -lc`, atau `cmd.exe /C` untuk target Windows). Token diambil dari argv mentah (`os.Args` di POSIX, `CommandLineToArgvW` di Windows).

- Argumen ber-spasi/kutipan di-re-quote POSIX-safe sehingga tetap **satu token** di remote: `make-sync exec docker inspect x --format '{{.Config.Image}} {{.State.Status}}'`.
- Operator (`|`, `>`, `>>`, `<`, `&&`, `||`, `;`, `&`, `2>&1`) dibiarkan telanjang agar diinterpretasi shell remote.
- Karena shell **lokal** sudah membuang quoting, variabel lokal ikut terekspansi. Untuk ekspansi di remote, bungkus dengan kutip: `make-sync exec 'echo $HOME'`.
- Jika `devsync.auth.remotePath` diisi, command otomatis diprefix `cd <remotePath> &&`.
- Tidak ada parsing flag lokal untuk `exec`; semua token setelah `exec` adalah perintah remote. Pengecualian: `-it`/`--it`/`-i`/`--interactive` memaksa mode PTY, dan perintah ber-`-it`/diakhiri `bash`/`sh`/`zsh` tanpa `-c` dideteksi interaktif otomatis.
- Mode interaktif butuh TTY nyata; tidak bisa dipakai saat stdin di-pipe (mis. dari agent/CI). Jika perlu output terprogram, pakai perintah non-interaktif.

## Alur kerja yang disarankan

1. `make-sync path-info` bila ada kecurigaan salah path/config/agent.
2. Untuk perubahan kecil: `make-sync pull --path <dir>` lalu `make-sync push --path <dir>` — hindari full-project sync tanpa alasan.
3. Untuk melihat status remote tanpa mengubah apa pun: `make-sync exec 'git status --short && git log -1 --oneline'`.
4. Untuk perintah yang butuh docker/kubectl interaktif: `make-sync exec docker compose exec -it app bash` (hanya dari terminal nyata).

## Troubleshooting

- **Tidak ada file tertransfer** — hash sama, atau path/pola tidak match. Cek scope `--path` dan hasil parse `.sync_ignore` (pesan `📄 Loaded .sync_ignore as YAML/.gitignore format`).
- **Indexing remote gagal** — cek kredensial SSH, `remotePath`, dan izin eksekusi agent (`chmod +x`). Sistem mencoba fallback binary di `.sync_temp` sebelum menyerah.
- **Config tidak valid** — `LoadAndRenderConfig` merender `=var.*` lalu validasi; error menyebut field yang gagal. Perbaiki nilai `var:` alih-alih meng-hardcode.
- **`go build ./...` gagal di Linux** — `cmd/wincmdline/wincmdline.go` meng-import `golang.org/x/sys/windows` tanpa `//go:build windows`, sehingga ikut dikompilasi di Linux. Tambahkan build tag itu (semua file platform lain di repo sudah benar). Build binary Linux cukup `go build .` di root, atau `GOOS=windows go build ./...` untuk target Windows.
- **`--version` tidak dikenal** — binary tidak punya flag versi; gunakan `make-sync --help` atau cek commit repo.
- **Pipeline** — fitur pipeline sudah dihapus dari rilis; `pipeline-sample.yaml` hanya arsip. Jangan menawarkan alur pipeline.

## Batasan yang harus disebut ke pengguna

- `--bypass-ignore` ikut mengirim file sensitif (`.env`, `.ssh`, `.sync_collections/**`). Jangan pakai tanpa persetujuan eksplisit.
- `--git-changes` ada di help tetapi dinonaktifkan; jangan menyarankannya — arahkan ke `deploy --since-commit <rev>` untuk scope berbasis commit.
- Repo memakai remote `git@github.com:rolldone/make-sync.git`; branch kerja `master`.
- Skill ini bersumber tunggal dari `.agents/skills/make-sync/SKILL.md` di dalam repo, dan di-symlink ke profil Hermes (`~/.hermes/skills/devops/make-sync`) serta `~/.agents/skills/make-sync`. Edit file di repo, bukan salinannya.
- `.gitignore` memuat pola telanjang `make-sync` (untuk binary hasil build), sehingga folder skill ikut ter-ignore. Tambahkan baris `!.agents/skills/make-sync/` bila skill perlu ikut ter-commit.
