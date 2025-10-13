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
- **Silent Mode**: Kontrol tampilan output per step untuk output yang lebih bersih
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

Penjelasan singkat (bahasa sehari-hari):

> **Penjelasan singkat (bahasa sehari-hari)**
>
> - Baris pertama: "Jika job `test` selesai dengan sukses, jalankan job `deploy`." Jadi `deploy` otomatis dijalankan hanya bila `test` sukses.
> - Baris kedua: "Jika job `deploy` gagal, jalankan job `rollback`." `rollback` adalah fallback otomatis saat `deploy` bermasalah.
>
> **Visual sederhana (urutan eksekusi)**
>
> - prepare
> - build
> - test
>   - jika test == success → deploy
>     - jika deploy == failure → rollback
>
> **Catatan penting**
>
> - `conditionals` di level pipeline dievaluasi setelah job sumber selesai. Mereka tidak menggantikan dependensi eksplisit (`depends_on`), melainkan menambahkan aturan pemicu berdasarkan hasil.
> - Jika Anda memakai `executions.jobs` di `make-sync.yaml`, engine hanya akan mempertimbangkan job yang tercantum di daftar tersebut. Pastikan job yang bisa dipicu oleh `conditionals` (mis. `deploy`, `rollback`) termasuk di `executions.jobs` agar tidak ter-skip.
> - Untuk debugging: sementara tes, jalankan execution tanpa `executions.jobs` atau sertakan semua job supaya tidak ada job yang tidak sengaja di-skip.

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

### Execution registration & jobs checklist

Saat mendaftarkan sebuah `execution` di `make-sync.yaml`, pastikan fields berikut tercantum agar pipeline dan `conditionals` bekerja dengan benar:

- `name` — nama yang mudah dibaca (mis. "Production Deploy").
- `key` — kunci singkat yang dipakai untuk menjalankan execution: `make-sync pipeline run <key>`.
- `pipeline` — nama file pipeline YAML (mis. `deploy_pipeline.yaml`) yang memuat definisi job/step/conditionals.
- `hosts` — daftar host/target untuk execution ini (dibutuhkan untuk SSH execution).
- `var` atau `variables` (opsional) — environment / overrides yang diperlukan oleh pipeline.
- `jobs` (opsional tetapi penting saat menggunakan `conditionals`) — daftar job yang ingin dijalankan untuk execution ini.

Kenapa `jobs` penting untuk `conditionals`:
- Jika `executions` menyertakan `jobs: [...]`, engine hanya akan mempertimbangkan job yang ada di daftar tersebut.
- Oleh karena itu, jika ada `conditionals` yang dapat memicu job lain (mis. `deploy` atau `rollback`), pastikan job target tersebut juga termasuk di `jobs` list.

Checklist praktis sebelum menjalankan execution:
- [ ] File `pipeline` ada dan path benar.
- [ ] Semua job yang direferensikan di `conditionals` ada di pipeline.
- [ ] Jika memakai `executions.jobs`, pastikan job target conditional termasuk di daftar itu.
- [ ] Job names unik dan ejaan tepat (case‑sensitive).
- [ ] Variables & hosts siap (SSH/auth) agar job dapat berjalan.

Contoh execution yang aman untuk flow build→test→deploy→rollback:

```yaml
executions:
  - name: "Production Deploy"
    key: "prod"
    pipeline: "deploy_pipeline.yaml"
    hosts: ["prod-server-01"]
    variables:
      SOME_FLAG: "on"
    jobs: [prepare, build, test, deploy, rollback, notify]  # include all possible targets
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
    timeout: 30                     # Timeout in seconds (default: 100)
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
| `description` | string | - | Human-readable description |
| `silent` | bool | false | Suppress real-time output display |
| `timeout` | int | 100 | Command timeout in seconds |
| `working_dir` | string | - | Override working directory for this step |
| `save_output` | string | - | Save command output to context variable |
| `conditions` | []Condition | - | Conditional execution based on output |
| `expect` | []Expect | - | Interactive prompt responses |

## Step Field: conditions (Conditional Execution)

Field `conditions` pada step pipeline memungkinkan branching atau aksi khusus berdasarkan output command. Dengan ini, pipeline bisa otomatis lanjut, lompat, atau melakukan error handling sesuai hasil eksekusi.

### Cara Kerja
- Setelah command dijalankan, outputnya dicek terhadap pola (regex atau string match) di `conditions`.
- Jika pola cocok, aksi (`action`) dijalankan, misal:
  - `continue`: lanjut ke step berikutnya
  - `drop`: hentikan eksekusi job/step
  - `goto_step`: lompat ke step tertentu
  - `goto_job`: lompat ke job tertentu
  - `fail`: tandai job sebagai gagal dan lanjut ke conditional/fallback (misal rollback).

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
- Gunakan `fail` untuk men-trigger fallback/rollback jika terjadi error.

## Penjelasan Mendalam tentang Conditionals

Bagian ini menjelaskan secara rinci bagaimana engine memproses `conditions` setelah sebuah step selesai menjalankan command. Tujuannya agar user paham urutan pengecekan, bentuk pencocokan pola, dan efek samping seperti loop atau branching.

1. Urutan evaluasi
  - Setelah sebuah step selesai, seluruh output command (stdout + stderr digabung) disatukan menjadi satu string buffer.
  - Engine mengevaluasi daftar `conditions` secara berurutan seperti ditulis di YAML. Pencocokan berhenti pada kecocokan pertama kecuali action yang memerintahkan loop (`goto_step` ke step yang sama) atau eksplisit lain.

2. Tipe pencocokan
  - `pattern` bisa berupa string biasa atau regular expression. Jika dimulai dan diakhiri dengan `/` (mis. `/error|fail/`), engine menganggapnya regex dan melakukan pencocokan regex; selain itu dilakukan pencocokan substring (case-sensitive).
  - Untuk mencocokkan case-insensitive, gunakan konstruksi regex dengan flag (?i), mis. `/(?i)error/`.

3. Capture groups dan variabel
  - Jika pattern berupa regex dan ada grup tangkapan (capture groups), engine menyimpan grup tersebut ke variable lokal step (mis. `{{__match_1}}`, `{{__match_2}}`) serta memasukkan keseluruhan match ke `{{__match}}`.
  - Untuk reuse yang lebih aman, gunakan `save_output` untuk menyimpan output penuh, lalu lakukan parsing di step selanjutnya.

4. Behaviour action
  - `continue`: lanjut ke step berikutnya seperti biasa.
  - `drop`: hentikan job saat ini (engine menandai job selesai tanpa error) — berguna untuk bail-out yang sah.
  - `goto_step`: lompat ke step target. Jika target sama dengan step saat ini, ini membuat loop — engine membatasi loop berulang secara default (lihat bagian Troubleshooting).
  - `goto_job`: lompat ke job target. Jika job target berada di luar `executions.jobs` (jika ditentukan), engine akan mengabaikannya dan menampilkan peringatan.
  - `fail`: tandai job sebagai gagal; ini memicu evaluasi `conditionals` top‑level pipeline (mis. rollback yang didefinisikan di `conditionals`).

5. Edge cases & safety
  - Jika beberapa kondisi cocok, hanya kondisi pertama (berdasarkan urutan YAML) yang dipakai — ini menghindari ambiguitas.
  - Loop detection: engine menghitung lompat (goto_step ke langkah yang sama) dan jika ambang (default 10) terlampaui, engine akan memicu `fail` untuk menghindari infinite loop.
  - Jika `goto_job` menunjuk job yang tidak ada, engine menandai job saat ini sebagai `fail` dan menampilkan error yang jelas.

6. Debugging tips
  - Untuk melihat teks lengkap yang dicocokkan, aktifkan step tanpa `silent` untuk melihat streaming atau tambahkan `save_output` lalu `echo` di step berikutnya.
  - Gunakan pattern regex eksplisit (`/pattern/`) ketika output lebih kompleks atau multiline.
  - Jika sebuah `goto_job` tampak tidak berjalan saat execution memakai `executions.jobs`, pastikan target job termasuk dalam daftar `jobs` untuk execution tersebut.

Contoh lanjutan:

```yaml
steps:
  - name: "health-check"
   type: "command"
   commands: ["curl -sS http://localhost:8080/health || true"]
   save_output: "health_out"
   conditions:
    - pattern: "/UP|OK/"
      action: "continue"
    - pattern: "/DOWN|UNHEALTHY/"
      action: "goto_job"
      job: "handle-unhealthy"
    - pattern: "/timeout/i"
      action: "fail"

  - name: "display-health"
   type: "command"
   commands: ["echo '{{health_out}}'"]
```


### Use Case
- Validasi hasil command (misal: ping, test, health check)
- Branching logic otomatis tanpa scripting manual
- Error handling yang lebih fleksibel

Field lain yang relevan:
- `pattern`: string/regex yang dicari di output
- `action`: aksi yang dijalankan jika pattern cocok
- `step`: target step (untuk `goto_step`)
- `job`: target job (untuk `goto_job`)

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
