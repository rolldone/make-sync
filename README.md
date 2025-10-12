# make-sync

CLI untuk sinkronisasi file/devsync via SSH, dengan mode aman (safe pull/push), menu interaktif, dan dukungan manual sync berbasis path terdaftar maupun pola `!` dari `.sync_ignore`.

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

## Instalasi & Build
- Butuh Go 1.24+.
- Jalankan:

```
go build ./...
```

Jika ingin menjalankan seluruh test (yang ada):

```
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
    - assets/images
```

Catatan:
- `devsync.auth.remote_path` adalah root project di remote.
- `devsync.manual_transfer` adalah daftar path yang ditampilkan di menu Manual/Single Sync.
- `.sync_ignore` di root mengatur pola ignore biasa, dan juga bisa memuat pola negasi `!pattern` untuk fitur include-by-pattern.

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

### Contoh Konfigurasi Lengkap:

```yaml
direct_access:
  config_file: ""  # Kosongkan untuk generate otomatis ke .sync_temp/.ssh/config
  ssh_configs:
    - Host: my-server
      HostName: 192.168.1.100
      User: username
      Port: "22"
      RemoteCommand: cmd /K cd =remotePath  # Untuk Windows target
      RequestTty: force
      StrictHostKeyChecking: "no"
      ServerAliveInterval: "300"
      ServerAliveCountMax: "2"
  ssh_commands:
    - access_name: Connect to Server
      command: ssh -v my-server
```

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
   - "All Data Only In Your \"Sync Ignore\" File Pattern" (berdasarkan `!patterns` dari `.sync_ignore`)
5. Pilih mode:
   - Rsync Soft Mode: hanya transfer file yang berubah, tanpa penghapusan
   - Rsync Force Mode: selain transfer, juga melakukan penghapusan file yang tidak ada pada sisi lain, terbatas pada scope pilihan

Selama Download/Upload:
- Sistem akan menjalankan indexing di remote terlebih dahulu (mirroring safe pull/push) agar DB up-to-date.

### Navigasi Keyboard di TUI
- Back bertahap: gunakan item menu "Back" untuk naik satu level.
- Keluar cepat: Esc, q, atau Ctrl+C akan keluar dari seluruh flow Single/Manual Sync.
- Catatan: Mode Force tidak menampilkan prompt konfirmasi.

## Detail Perilaku
- Download Soft: unduh file remote yang belum ada atau hash berbeda, dalam scope.
- Download Force: setelah unduh, hapus file lokal yang match scope namun tidak ada di DB remote.
- Upload Soft: unggah file lokal yang belum ada atau hash berbeda, dalam scope.
- Upload Force: tandai setiap file lokal yang diproses sebagai `checked` (di DB bila tersedia), lalu hapus file remote match scope yang tidak `checked`.
- Scope "Sync Ignore !patterns":
  - Matcher dibangun dengan basis ignore semua (`**`), lalu di-"unignore" oleh tiap negation pattern (ditambah varian `**/` untuk token pendek).
  - `.sync_temp` selalu diabaikan.
  - Pola ignore `.sync_ignore` tetap dihormati saat traversing.

## Tips & Batasan
- Pastikan `devsync.auth` terisi benar untuk koneksi SSH.
- Jika build agent gagal, sistem akan mencoba fallback binary di folder `.sync_temp` sesuai target OS.
- Force Mode tidak menampilkan prompt konfirmasi. Pastikan scope sudah tepat sebelum mengeksekusi.
- Untuk Windows remote, penghapusan file menggunakan `cmd.exe /C del /f /q`.

### Catatan tentang `.sync_temp` dan ignore
- `.sync_temp` selalu dikecualikan dari sinkronisasi.
- Pola di `.sync_ignore` tetap dihormati saat traversal lokal.

## Eksekusi Perintah Remote (exec)

Jalankan perintah langsung di host remote tanpa PTY. Output di-stream real-time; selesai → proses exit dengan status yang sesuai.

Penggunaan:

```
make-sync exec <command...>
```

Perilaku:
- Bekerja dari direktori kerja remote: `devsync.auth.remote_path` (fallback `remote_path`). Jika kosong, tidak melakukan `cd`.
- Pembungkus shell otomatis:
  - Linux/Unix: `bash -lc 'cd <remotePath> && <command>'`
  - Windows: `cmd.exe /C "cd /d <remotePath> && <command>"`
- Tidak ada flag lokal untuk `exec` (DisableFlagParsing). Semua argumen setelah `exec` diteruskan apa adanya ke remote shell.
- Non-PTY: tidak cocok untuk perintah yang butuh TTY/prompt interaktif.
- Exit code: 0 jika sukses; non-zero jika eksekusi gagal.

Contoh:
- Tampilkan file tersembunyi (Linux/Unix):
  - `make-sync exec ls -a -l`
- Docker compose restart (semua target OS):
  - `make-sync exec docker compose down && docker compose up`
- Menjalankan beberapa perintah berantai:
  - `make-sync exec mkdir -p release && cd release && ls`

Tips quoting:
- Windows: operator seperti `&&`, `|`, atau path dengan spasi tetap didukung karena dibungkus `cmd.exe /C`. Jika ada karakter kutip ganda di command, pertimbangkan untuk mengutip ulang bagian terkait.
- Linux/Unix: command dibungkus ke `bash -lc '...'`. Jika Anda perlu menyertakan `'` di dalam command, gunakan escape `'\''` atau beralih ke kutip ganda dengan hati-hati.

Troubleshooting:
- "command not found": pastikan perintah tersedia di PATH remote, atau gunakan path absolut/bin yang tepat.
- Salah direktori kerja: set `devsync.auth.remote_path` (atau `remote_path`) pada config agar `cd` otomatis ke root project di remote.

## Pipeline System

make-sync menyediakan sistem pipeline yang powerful untuk menjalankan workflow otomatisasi CI/CD dengan dukungan SSH, variable persistence, dan conditional execution.

### Fitur Pipeline

- **Job Dependencies**: Definisi dependensi antar job untuk eksekusi berurutan
- **Variable Persistence**: Simpan output command ke variable yang bisa digunakan job berikutnya
- **Conditional Execution**: Jalankan job berdasarkan success/failure job sebelumnya
- **SSH Execution**: Jalankan command di remote server via SSH
- **Subroutine Calls**: Panggil job tertentu menggunakan `goto_job` untuk error handling
- **Template Generation**: Buat pipeline template Docker-focused dengan mudah

### Membuat Pipeline Baru

Gunakan command `pipeline create` untuk membuat template pipeline baru:

```bash
# Dari directory dengan konfigurasi pipeline_dir
make-sync pipeline create my-app

# Akan membuat file di pipeline_dir (default: .sync_pipelines/my-app.yaml)
```

Template yang dihasilkan mencakup:
- **5 Jobs**: prepare, build, test, deploy, rollback
- **Docker Integration**: Build, test, dan deploy aplikasi container
- **Variable Management**: Persistent variables untuk tracking build info
- **Error Handling**: Conditional rollback pada failure
- **SSH Deployment**: Multi-host deployment support

### Struktur Pipeline

```yaml
pipeline:
  name: "my-app"
  description: "Docker-based CI/CD pipeline"
  strict_variables: false

  variables:
    DOCKER_REGISTRY: "docker.io"
    DOCKER_REPO: "your-org/my-app"
    DOCKER_TAG: "latest"

  jobs:
    - name: "prepare"
      steps:
        - name: "check-docker"
          type: "command"
          commands: ["docker --version"]

    - name: "build"
      depends_on: ["prepare"]
      steps:
        - name: "build-image"
          type: "command"
          commands: ["docker build -t {{DOCKER_REGISTRY}}/{{DOCKER_REPO}}:{{DOCKER_TAG}} ."]
          save_output: "image_id"

  conditionals:
    - job: "deploy"
      condition: "success"
      when: "test"
    - job: "rollback"
      condition: "failure"
      when: "deploy"

executions:
  - name: "Development Build"
    key: "dev"
    pipeline: "my-app.yaml"
    var: "dev"                    # Reference ke vars.yaml
    hosts: ["localhost"]

  - name: "Production Deploy"
    key: "prod"
    pipeline: "my-app.yaml"
    var: "prod"                   # Reference ke vars.yaml
    hosts: ["prod-server-01", "prod-server-02"]
```

### Menjalankan Pipeline

```bash
# List available executions
make-sync pipeline list

# Run specific execution
make-sync pipeline run dev
make-sync pipeline run prod

# Override variables via CLI
make-sync pipeline run prod --var DOCKER_TAG=v1.2.3
make-sync pipeline run dev --var DOCKER_TAG=dev-test --var BUILD_ENV=development

# Dynamic variables untuk CI/CD
make-sync pipeline run prod --var DOCKER_TAG=$(git rev-parse --short HEAD)
make-sync pipeline run dev --var DOCKER_TAG=dev-$(date +%Y%m%d-%H%M%S)
```

### Variable Override CLI

Pipeline mendukung variable override langsung dari command line menggunakan flag `--var`:

#### Syntax
```bash
# Single variable
make-sync pipeline run [execution_key] --var KEY=value

# Multiple variables
make-sync pipeline run [execution_key] --var KEY1=value1 --var KEY2=value2
```

#### Contoh Penggunaan
```bash
# Override DOCKER_TAG untuk production
make-sync pipeline run prod --var DOCKER_TAG=v1.2.3

# Override multiple variables
make-sync pipeline run dev --var DOCKER_TAG=dev-test --var BUILD_ENV=development

# Dynamic values untuk CI/CD
make-sync pipeline run prod --var DOCKER_TAG=$(git rev-parse --short HEAD)
make-sync pipeline run staging --var DOCKER_TAG=staging-$(date +%Y%m%d-%H%M%S)

# Useful untuk testing
make-sync pipeline run dev --var DEBUG=true --var LOG_LEVEL=debug
```

### Variable Interpolation

Pipeline mendukung variable interpolation dengan format `{{VAR_NAME}}` atau `${{VAR_NAME}}`:

- **CLI Overrides**: Variables dari `--var` flag (prioritas tertinggi)
- **Vars File**: Variables dari `vars.yaml` berdasarkan `execution.var` key
- **Global Variables**: Didefinisikan di `pipeline.variables`
- **Runtime Variables**: Disimpan menggunakan `save_output` dari command

### Variables File System (vars.yaml)

Pipeline menggunakan sistem vars.yaml untuk mengelola variables per environment. Sistem ini bekerja dengan field `var` di executions.

#### Struktur vars.yaml
```yaml
# File: .sync_pipelines/vars.yaml
global:
  username: donny
  host: 192.168.1.100

dev:
  DOCKER_TAG: "dev-$(date +%Y%m%d-%H%M%S)"
  BUILD_ENV: "development"
  DEBUG: "true"

staging:
  DOCKER_TAG: "staging-latest"
  BUILD_ENV: "staging"
  DEBUG: "false"

prod:
  DOCKER_TAG: "$(git rev-parse --short HEAD)"
  BUILD_ENV: "production"
  DEBUG: "false"
```

#### Penggunaan dengan Executions
```yaml
# Di make-sync.yaml
executions:
  - name: "Development Build"
    key: "dev"
    pipeline: "my-app.yaml"
    var: "dev"                    # Menggunakan variables dari key "dev" di vars.yaml
    hosts: ["localhost"]

  - name: "Staging Deploy"
    key: "staging"
    pipeline: "my-app.yaml"
    var: "staging"                # Menggunakan variables dari key "staging"
    hosts: ["staging-server"]
```

#### Priority Variables
Ketika menjalankan pipeline, variables akan di-merge dengan priority:
1. **CLI Overrides** (`--var KEY=value`) - Tertinggi
2. **Execution Variables** (`execution.variables` - fitur baru)
3. **vars.yaml** (berdasarkan `execution.var`)
4. **Global pipeline variables**
5. **Runtime variables** (dari `save_output`)

### Direct Variables (execution.variables)

Selain sistem vars.yaml, Anda juga bisa mendefinisikan variables langsung di execution menggunakan field `variables`. Fitur ini memberikan fleksibilitas lebih tanpa perlu membuat file vars.yaml terpisah.

#### Syntax
```yaml
executions:
  - name: "Development Build"
    key: "dev"
    pipeline: "my-app.yaml"
    variables:                    # Direct variables definition
      DOCKER_TAG: "dev-$(date +%Y%m%d-%H%M%S)"
      BUILD_ENV: "development"
      DEBUG: "true"
    hosts: ["localhost"]
```

#### Semua Pendekatan Variables

**1. Menggunakan vars.yaml (sistem lama - tetap didukung)**
```yaml
executions:
  - name: "Development Build"
    key: "dev"
    pipeline: "my-app.yaml"
    var: "dev"                    # Reference ke vars.yaml
    hosts: ["localhost"]
```

**2. Menggunakan variables langsung (fitur baru)**
```yaml
executions:
  - name: "Development Build"
    key: "dev"
    pipeline: "my-app.yaml"
    variables:                    # Direct definition
      DOCKER_TAG: "dev-$(date +%Y%m%d-%H%M%S)"
      BUILD_ENV: "development"
    hosts: ["localhost"]
```

**3. Hybrid approach (kombinasi keduanya)**
```yaml
executions:
  - name: "Development Build"
    key: "dev"
    pipeline: "my-app.yaml"
    var: "dev"                    # Base variables dari vars.yaml
    variables:                    # Override specific variables
      DOCKER_TAG: "custom-dev-tag"
      NEW_FEATURE: "enabled"
    hosts: ["localhost"]
```

#### Use Cases

- **vars.yaml**: Ideal untuk shared variables antar executions dan secret management
- **variables**: Ideal untuk execution-specific overrides dan simple configurations
- **hybrid**: Ideal untuk complex setups dengan base + customization

### Advanced Features

#### Variable Persistence
```yaml
steps:
  - name: "get-version"
    type: "command"
    commands: ["echo 'v1.2.3'"]
    save_output: "app_version"  # Simpan output ke variable

  - name: "deploy"
    type: "command"
    commands: ["echo 'Deploying version {{app_version}}'"]  # Gunakan variable
```

#### Conditional Execution
```yaml
conditionals:
  - job: "deploy"
    condition: "success"
    when: "test"        # Jalankan deploy jika test sukses

  - job: "rollback"
    condition: "failure"
    when: "deploy"      # Jalankan rollback jika deploy gagal
```

#### Subroutine Calls
```yaml
steps:
  - name: "check-condition"
    type: "command"
    commands: ["echo 'SUCCESS'"]
    conditions:
      - pattern: "SUCCESS"
        action: "goto_job"
        job: "success_handler"
```

### Konfigurasi Pipeline Directory

Pipeline disimpan di direktori yang dikonfigurasi di `make-sync.yaml`:

```yaml
direct_access:
  pipeline_dir: ".sync_pipelines"  # Default location
```

Jika `pipeline_dir` tidak dikonfigurasi, pipeline akan disimpan di working directory saat command dijalankan.

### File Sistem Pipeline

Pipeline menggunakan struktur file berikut:
- `pipeline_dir/*.yaml`: File definisi pipeline
- `pipeline_dir/vars.yaml`: Variable global (auto-generated)
- `pipeline_dir/scripts/`: Script files untuk eksekusi kompleks

**⚠️ Reserved Names**: Jangan gunakan nama `vars` atau `scripts` untuk pipeline karena akan konflik dengan file sistem.

## Troubleshooting
- Tidak ada file yang tertransfer:
  - Cek apakah hash sama atau scope/pola tidak match.
- Gagal indexing remote:
  - Periksa kredensial SSH, jalur `remote_path`, dan izin eksekusi pada binary agent (Unix: chmod +x).
- Pola `!pattern` tidak bekerja:
  - Pastikan `.sync_ignore` ada di root lokal, setiap pattern pada baris terpisah, tanpa komentar. Gunakan `**/` untuk menjangkau subfolder jika perlu.

## Lisensi
Proyek ini mengikuti lisensi yang ditentukan oleh pemilik repositori.
