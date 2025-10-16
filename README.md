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
- **Real-time Output Streaming**: Tampilkan output command secara real-time saat eksekusi
- **Pipeline Logging**: Semua output otomatis tersimpan ke log file dengan timestamp
- **Silent Mode**: Kontrol tampilan output per step untuk output yang lebih bersih
- **Timeout Control**: Dual timeout system (idle + total) untuk kontrol eksekusi yang lebih cerdas
- **Subroutine Calls**: Panggil job tertentu menggunakan `goto_job` untuk error handling
- **Template Generation**: Buat pipeline template Docker-focused dengan mudah
- **Job Execution Tracking**: Visual indicators dengan HEAD markers untuk tracking progress

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
      mode: "local"        # Run steps locally (no SSH)
      steps:
        - name: "check-docker"
          type: "command"
          commands: ["docker --version"]

    - name: "build"
      mode: "local"        # Run steps locally
      depends_on: ["prepare"]
      steps:
        - name: "build-image"
          type: "command"
          commands: ["docker build -t {{DOCKER_REGISTRY}}/{{DOCKER_REPO}}:{{DOCKER_TAG}} ."]
          save_output: "image_id"

    - name: "deploy"
      mode: "remote"       # Run steps via SSH (default)
      depends_on: ["build"]
      steps:
        - name: "upload-files"
          type: "file_transfer"
          source: "dist/"
          destination: "/var/www/app/"
        - name: "restart-service"
          type: "command"
          commands: ["systemctl restart myapp"]

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

### Job Configuration

Jobs dalam pipeline dapat dikonfigurasi dengan mode eksekusi:

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `name` | string | - | Unique job identifier |
| `depends_on` | []string | - | Jobs that must complete before this job runs |
| `mode` | string | "remote" | Execution mode: `"local"` or `"remote"` |
| `steps` | []Step | - | Steps to execute in this job |

#### Job Execution Modes

- **`local`**: Semua step dijalankan secara lokal tanpa SSH
  - Command steps: Jalankan di mesin lokal
  - File transfer steps: Copy file lokal (bukan upload via SCP)
  - Script steps: Jalankan script lokal

- **`remote`** (default): Semua step dijalankan via SSH ke remote host
  - Command steps: Jalankan via SSH
  - File transfer steps: Upload/download via SCP
  - Script steps: Upload dan jalankan script via SSH

#### Example Usage

```yaml
jobs:
  - name: "local-build"
    mode: "local"
    steps:
      - name: "install-deps"
        commands: ["npm install"]
      - name: "copy-assets"
        type: "file_transfer"
        source: "src/assets/"
        destination: "dist/assets/"

  - name: "remote-deploy"
    mode: "remote"
    steps:
      - name: "upload-app"
        type: "file_transfer"
        source: "dist/"
        destination: "/var/www/app/"
```

### Step Configuration

Setiap step dalam pipeline mendukung konfigurasi advanced untuk kontrol eksekusi yang lebih detail:

#### Basic Step Structure
```yaml
steps:
  - name: "build-app"
    type: "command"
    commands: ["npm run build"]
    description: "Build the application"
```

#### Advanced Step Configuration
```yaml
steps:
  - name: "ping-test"
    type: "command"
    commands: ["ping -c 5 google.com"]
    description: "Test network connectivity"
    silent: true                    # Suppress real-time output display
    timeout: 30                     # Total timeout in seconds (default: 0 = unlimited)
    idle_timeout: 300               # Idle timeout in seconds (default: 600 = 10 minutes)
    working_dir: "/app"             # Override working directory
    save_output: "ping_result"      # Save command output to variable
    conditions:                     # Conditional execution
      - pattern: "5 packets"
        action: "continue"
```

#### Step Configuration Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `name` | string | - | Unique step identifier |
| `type` | string | - | Step type: `command`, `file_transfer`, `script` |
| `commands` | []string | - | Commands to execute (for `command` type) |
| `file` | string | - | Script file path (for `script` type) |
| `source` | string | - | Source path (for `file_transfer` type) |
| `destination` | string | - | Destination path (for `file_transfer` type) |
| `direction` | string | "upload" | Transfer direction: `upload` or `download` |
| `template` | string | - | Template rendering: `"enabled"` to render `{{variables}}` in file content |
| `description` | string | - | Human-readable description |
| `silent` | bool | false | Suppress real-time output display |
| `timeout` | int | 0 | Total timeout in seconds (0 = unlimited) |
| `idle_timeout` | int | 600 | Idle timeout in seconds (10 minutes) - resets on output activity |
| `working_dir` | string | - | Override working directory for this step |
| `save_output` | string | - | Save command output to context variable |
| `conditions` | []Condition | - | Conditional execution based on output |
| `expect` | []Expect | - | Interactive prompt responses |

### Per-file transfers with `files` (new)

You can provide multiple files in a single `file_transfer` step using the `files` array. Each entry can specify its own `source`, an optional per-file `destination`, and an optional `template` override. When `files` is present it takes priority over the older `source` / `sources` fields.

Example:

```yaml
- name: "upload-files"
  type: "file_transfer"
  files:
    - source: "xxx.txt"
      destination: "/xxx/xxx/xxx.txt"
    - source: "vvv.txt"
      destination: "/xxx/xxx/vvv.txt"
    - source: "dist/"
      destination: "/xxx/dist/"
```

Behavior notes:
- If `files` is present, it overrides `source` and `sources` for that step.
- Each file entry may provide its own `destination`; if omitted, the step-level `destination` is used.
- Globs in `source` (e.g. `dist/**/*.js`) are expanded locally and each match is transferred preserving relative paths under the destination directory.
- `template: "enabled"` may be set per-step or per-file; per-file `template` overrides the step-level setting.
- If multiple files are provided and the resolved destination is a directory (ends with `/` or exists as a directory), all files will be placed under that directory. If multiple files are provided but destination looks like a single file path, the step will error.
- Backwards compatibility: existing single-file `source` usage still works if `files` is not present.

### Timeout Configuration

Pipeline mendukung dua jenis timeout untuk kontrol eksekusi yang lebih fleksibel:

#### Total Timeout (`timeout`)
- **Default**: 0 (unlimited)
- **Behavior**: Total waktu maksimal eksekusi command
- **Use case**: Mencegah command yang benar-benar stuck berjalan terlalu lama

#### Idle Timeout (`idle_timeout`) 
- **Default**: 600 seconds (10 minutes)
- **Behavior**: Waktu maksimal tanpa aktivitas output - timer reset setiap ada output baru
- **Use case**: Ideal untuk command yang memberikan progress feedback (download, build, dll)

```yaml
steps:
  - name: "download-large-file"
    type: "command"
    commands: ["wget https://example.com/large-file.zip"]
    timeout: 3600        # Total timeout: 1 hour max
    idle_timeout: 300   # Idle timeout: 5 minutes (resets on download progress)
    
  - name: "compile-project"
    type: "command" 
    commands: ["make all"]
    timeout: 0           # No total timeout limit
    idle_timeout: 600    # 10 minutes idle timeout (resets on compiler output)
```

**Timeout Logic**:
- Command akan timeout jika melebihi **total timeout** ATAU tidak ada output selama **idle timeout**
- Timer idle otomatis reset setiap kali command menghasilkan output
- Total timeout 0 = unlimited (hanya idle timeout yang berlaku)

## Step Field: conditions (Conditional Execution)

Field `conditions` pada step pipeline memungkinkan branching atau aksi khusus berdasarkan output command. Dengan ini, pipeline bisa otomatis lanjut, lompat, atau melakukan error handling sesuai hasil eksekusi.

### Cara Kerja
- Setelah command dijalankan, outputnya dicek terhadap pola (regex atau string match) di `conditions`.
- Jika pola cocok, aksi (`action`) dijalankan, misal:
  - `continue`: lanjut ke step berikutnya
  - `drop`: hentikan eksekusi job (step berikutnya tidak dijalankan, job dianggap selesai tanpa error)
  - `goto_step`: lompat ke step tertentu
  - `goto_job`: lompat ke job tertentu
  - `fail`: tandai job sebagai gagal.

### Contoh YAML
```yaml
steps:
  - name: "check-status"
    type: "command"
    commands: ["curl -s http://service/health"]
    conditions:
      - pattern: "OK"
        action: "continue"
      - pattern: "ERROR"
        action: "goto_job"
        job: "handle-error"
      - pattern: "TIMEOUT"
        action: "fail"
      - pattern: "RETRY"
        action: "goto_step"
        step: "check-status"
      - pattern: "DROP"
        action: "drop"
```

**Penjelasan singkat:**
- Gunakan `continue` untuk flow normal.
- Gunakan `drop` untuk menghentikan job jika output tidak sesuai.
- Gunakan `goto_step`/`goto_job` untuk branching/loop/error handling.

## Step Field: template (File Template Rendering)

Field `template` pada step `file_transfer` mengontrol apakah file akan di-render dengan variable substitution sebelum di-upload.

### Cara Kerja
- Jika `template: "enabled"`, maka `{{variable}}` di dalam file content akan di-replace dengan nilai variable
- Jika field `template` tidak didefine, file akan di-upload as-is tanpa rendering
- Variable berasal dari execution variables, context variables, atau config variables

### Contoh Penggunaan

**File template (config.php):**
```php
<?php
$db_host = "{{db_host}}";
$db_name = "{{db_name}}";
$env = "{{environment}}";
```

**Pipeline step:**
```yaml
jobs:
  - name: deploy
    steps:
      - name: upload_config
        type: file_transfer
        source: "config.php"
        destination: "/var/www/config.php"
        template: "enabled"  # ← Enable template rendering
```

**Execution variables:**
```yaml
executions:
  - name: "Deploy Production"
    key: deploy-prod
    pipeline: deploy.yaml
    variables:
      db_host: "prod-db.example.com"
      db_name: "myapp_prod"
      environment: "production"
```

**Hasil file yang di-upload:**
```php
<?php
$db_host = "prod-db.example.com";
$db_name = "myapp_prod";
$env = "production";
```

### Catatan
- Template rendering aman dan hanya mengganti text `{{variable}}` dengan nilai
- Jika variable tidak ditemukan, text `{{variable}}` tetap tidak berubah
- Untuk file binary (images, JAR, dll), jangan set `template: "enabled"`
- Gunakan `fail` untuk men-trigger fallback/rollback jika terjadi error.

### Else Condition

Jika tidak ada `conditions` yang match dengan output command, Anda bisa menentukan `else_action` yang akan dijalankan:

```yaml
steps:
  - name: "check-status"
    type: "command"
    commands: ["curl -s http://service/health"]
    conditions:
      - pattern: "OK"
        action: "continue"
      - pattern: "ERROR"
        action: "fail"
    else_action: "goto_job"    # Dijalankan jika tidak OK dan tidak ERROR
    else_job: "handle-unknown" # Target job untuk else_action
```

**Field Else:**
- `else_action`: Action jika tidak ada conditions yang match ("continue", "drop", "goto_step", "goto_job", "fail")
- `else_step`: Target step untuk `else_action: "goto_step"`
- `else_job`: Target job untuk `else_action: "goto_job"`

Untuk workflow CI/CD yang kompleks, fitur ini sangat powerful dan memudahkan automation tanpa perlu scripting tambahan.

#### Silent Mode
`silent: true` menonaktifkan tampilan output real-time per baris, namun output tetap tersimpan dan bisa ditampilkan di step berikutnya:

```yaml
jobs:
  - name: "test"
    steps:
      - name: "run-tests"
        type: "command"
        commands: ["npm test"]
        silent: true              # No real-time output
        save_output: "test_output"
      
      - name: "show-results"
        type: "command"
        commands: ["echo 'Test Results: {{test_output}}'"]
        # Will display the full test output at once
```

#### Output Saving & Context Variables
Gunakan `save_output` untuk menyimpan hasil command ke variable yang bisa digunakan di step/job berikutnya:

```yaml
steps:
  - name: "get-version"
    type: "command"
    commands: ["git rev-parse HEAD"]
    save_output: "commit_sha"
    
  - name: "build-image"
    type: "command"
    commands: ["docker build -t myapp:{{commit_sha}} ."]
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

### Real-time Output Streaming

Pipeline menampilkan output command secara real-time saat eksekusi, memberikan visibility penuh terhadap progress command yang sedang berjalan:

```
▶️  EXECUTING JOB: deploy (HEAD)
📋 Executing step: ping-check
Running on 100.96.182.47: ping -c 3 google.com
Command output: PING google.com (216.239.38.120) 56(84) bytes of data.
Command output: 64 bytes from any-in-2678.1e100.net (216.239.38.120): icmp_seq=1 ttl=128 time=30.0 ms
Command output: 64 bytes from any-in-2678.1e100.net (216.239.38.120): icmp_seq=2 ttl=128 time=30.8 ms
Command output: 64 bytes from any-in-2678.1e100.net (216.239.38.120): icmp_seq=3 ttl=128 time=31.1 ms
Command output: 
Command output: --- google.com ping statistics ---
Command output: 3 packets transmitted, 3 received, 0% packet loss, time 1998ms
📋 Executing step: deploy-app
Running on 100.96.182.47: docker-compose up -d
Command output: Creating network "myapp_default" with the default driver
Command output: Creating myapp_web_1 ... done
Command output: Creating myapp_db_1 ... done
✅ Completed job: deploy
```

#### Silent Mode untuk Output Bersih
Untuk command yang verbose, gunakan `silent: true` untuk menekan output real-time dan tampilkan hasil akhir saja:

```yaml
steps:
  - name: "verbose-command"
    type: "command"
    commands: ["ping -c 10 google.com"]
    silent: true
    save_output: "ping_result"
    
  - name: "display-result"
    type: "command"
    commands: ["echo 'Ping completed: {{ping_result}}'"]
```

### Job Execution Tracking

Pipeline menampilkan progress eksekusi dengan visual indicators:
- **▶️ EXECUTING JOB: [name] (HEAD)**: Job sedang dieksekusi
- **📋 Executing step: [name]**: Step dalam job sedang berjalan
- **✅ Completed job: [name]**: Job berhasil diselesaikan
- **❌ Failed job: [name]**: Job gagal

### Pipeline Logging

Setiap eksekusi pipeline otomatis mencatat semua output ke file log untuk debugging dan audit trail.

#### Log File Location
```
.sync_temp/
  └── logs/
      ├── watcher.log                           # Application/watcher logs
      └── {pipeline-name}-{timestamp}.log       # Pipeline execution logs
```

#### Log Features
- **Automatic**: No configuration required - logs dibuat otomatis saat pipeline run
- **Timestamped**: Setiap baris dengan format `[YYYY-MM-DD HH:MM:SS]`
- **Clean Output**: ANSI escape codes di-strip untuk readability
- **Comprehensive**: Capture semua stdout/stderr dari command execution
- **Persistent**: Logs tidak hilang setelah pipeline selesai

#### Error evidence (in-memory history)

make-sync menyimpan ringkasan output terbaru selama eksekusi dalam buffer in-memory berkapasitas 300 KB (307200 bytes). Buffer ini bersifat FIFO: ketika total ukuran semua baris melebihi 300 KB, baris tertua akan di-evict sampai total ukuran kembali di bawah batas. Jika sebuah step gagal (mis. timeout atau error), seluruh isi buffer (hingga batas 300 KB) akan ditulis ke file log pipeline sebagai "error evidence" untuk membantu debugging. Baris tunggal yang lebih besar dari 300 KB tidak otomatis dipotong.

#### Controlling real-time logging with `log_output`

You can control whether a step's real-time output is written into the persistent pipeline log file using the `log_output` boolean flag. Logging is opt-in and resolved with the following priority (highest → lowest):

- Step-level `log_output`
- Job-level `log_output`
- Pipeline-level `log_output`

That means a `log_output: true` set on a Step will enable writing that step's output to the pipeline log even if the Job or Pipeline-level flag is `false` or unset. Conversely, setting `log_output: false` on a Step will suppress writing that Step's output even if the Job or Pipeline-level flags are `true`.

Important notes:
- The in-memory 300 KB error-evidence buffer always captures command output regardless of `log_output`. This ensures we can flush recent output on errors even when file logging is disabled for normal runs.
- `log_output` only controls whether output is written in real time to the pipeline log file and displayed during execution. It does not affect saving output to variables via `save_output`.

Example YAML snippets:

Step-level (enable only this step):

```yaml
steps:
  - name: "verbose-step"
    type: "command"
    commands: ["make test"]
    log_output: true
```

Job-level (enable for all steps in the job unless overridden by a step):

```yaml
jobs:
  - name: "build"
    log_output: true
    steps:
      - name: "run-tests"
        type: "command"
        commands: ["npm test"]
```

Pipeline-level (default for all jobs/steps in the pipeline unless overridden):

```yaml
pipeline:
  name: "ci"
  log_output: false  # disable file logging by default; enable selectively via job/step
```

Use `log_output` to reduce log noise for very verbose commands, while relying on the in-memory error-evidence buffer to collect recent output in case of failures.


#### Example Log File
```log
=== Pipeline: deploy-app ===
Started: 2025-10-15-08-30-00

[2025-10-15 08:30:00] ▶️  EXECUTING JOB: prepare (HEAD)
[2025-10-15 08:30:01] Docker version 20.10.7, build f0df350
[2025-10-15 08:30:02] Building image...
[2025-10-15 08:30:15] Successfully built abc123def456
```

#### Use Cases
```bash
# Review output after execution
tail -f .sync_temp/logs/my-pipeline-*.log

# Search for errors
grep -i "error" .sync_temp/logs/deploy-*.log

# Archive logs for compliance
aws s3 cp .sync_temp/logs/ s3://bucket/pipeline-logs/ --recursive
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

#### Fields Supporting Variable Interpolation

Variable interpolation didukung di field-field berikut:

- `commands` - Command yang akan dieksekusi
- `file` - Path file script (untuk `script` type)
- `source` - Source path (untuk `file_transfer` type)
- `destination` - Destination path (untuk `file_transfer` type)
- `working_dir` - Working directory untuk step
- `conditions[].pattern` - Regex pattern untuk conditional matching
- `expect[].response` - Response untuk interactive prompts

#### Direct Variables (execution.variables)

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
