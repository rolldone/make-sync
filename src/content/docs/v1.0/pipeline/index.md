---
title: "Pipeline"
---

## Fitur Pipeline

- **Job Dependencies**: definisikan dependensi antar job untuk eksekusi berurutan.
- **Variable Persistence**: simpan output perintah ke variabel untuk job selanjutnya.
- **SSH Execution**: eksekusi perintah di remote via SSH.
- **Real-time Output Streaming**: tampilkan output command secara real-time saat eksekusi.
- **Pipeline Logging**: semua output otomatis tersimpan ke log file dengan timestamp.
- **Silent Mode**: kontrol tampilan output per step untuk output yang lebih bersih.
- **Timeout Control**: dual timeout system (idle + total) untuk kontrol eksekusi yang lebih cerdas.
- **Subroutine Calls**: gunakan `goto_job` untuk error handling.
- **Template Generation**: buat template pipeline Docker-focused.
- **Variable Override**: override variables via CLI dengan flag `--var`.
- **Multi-Environment**: support multiple environment dengan vars.yaml.
- **Job Execution Tracking**: visual indicators dengan HEAD markers untuk tracking progress.

## Membuat Pipeline Baru

```bash
# Buat pipeline baru dengan template
make-sync pipeline create my-app

# File dibuat di .sync_pipelines/my-app.yaml
# Dengan 5 jobs: prepare, build, test, deploy, rollback
```

Template yang dihasilkan mencakup:
- **Docker Integration**: Build, test, dan deploy aplikasi container
- **Variable Management**: Variables untuk tracking build info  
- **Error Handling**: Rollback pada failure
- **SSH Deployment**: Multi-host deployment support

## Struktur Pipeline

```yaml
pipeline:
  name: "my-app"
  description: "Docker-based CI/CD pipeline"
  strict_variables: false

  # Global variables
  variables:
    DOCKER_REGISTRY: "docker.io"
    DOCKER_REPO: "your-org/my-app"
    DOCKER_TAG: "latest"

  jobs:
    - name: "prepare"
      mode: "local"        # Run locally
      steps:
        - name: "check-tools"
          type: "command"
          commands: ["docker --version"]

    - name: "build"
      mode: "local"        # Run locally
      depends_on: ["prepare"]
      steps:
        - name: "build-image"
          type: "command"
          commands: ["docker build -t {{DOCKER_REGISTRY}}/{{DOCKER_REPO}}:{{DOCKER_TAG}} ."]
          save_output: "image_id"

    - name: "deploy"
      mode: "remote"       # Run via SSH (default)
      depends_on: ["build"]
      steps:
        - name: "upload-files"
          type: "file_transfer"
          source: "dist/"
          destination: "/var/www/app/"
        - name: "restart-service"
          type: "command"
          commands: ["systemctl restart myapp"]
```

## Job Configuration

Jobs dalam pipeline dapat dikonfigurasi dengan mode eksekusi untuk menentukan apakah step dijalankan lokal atau remote.

### Job Configuration Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `name` | string | - | Unique job identifier |
| `depends_on` | []string | - | Jobs that must complete before this job runs |
| `mode` | string | "remote" | Execution mode: `"local"` or `"remote"` |
| `steps` | []Step | - | Steps to execute in this job |

### Job Execution Modes

#### Local Mode (`mode: "local"`)
Semua step dalam job dijalankan secara lokal di mesin yang menjalankan make-sync:
- **Command steps**: Jalankan command di local shell
- **File transfer steps**: Copy file/directory lokal (bukan upload via SCP)
- **Script steps**: Jalankan script lokal

#### Remote Mode (`mode: "remote"`) - Default
Semua step dalam job dijalankan via SSH ke remote host:
- **Command steps**: Jalankan command via SSH
- **File transfer steps**: Upload/download via SCP
- **Script steps**: Upload script ke remote dan jalankan via SSH

### Example Usage

```yaml
jobs:
  - name: "local-development"
    mode: "local"
    steps:
      - name: "install-deps"
        type: "command"
        commands: ["npm install"]
      - name: "copy-config"
        type: "file_transfer"
        source: "config/dev.yaml"
        destination: "dist/config.yaml"

  - name: "remote-production"
    mode: "remote"
    steps:
      - name: "deploy-app"
        type: "file_transfer"
        source: "dist/"
        destination: "/var/www/app/"
      - name: "restart-services"
        type: "command"
        commands: ["systemctl restart nginx", "systemctl restart app"]
```

## Step Configuration

Setiap step dalam pipeline mendukung konfigurasi advanced untuk kontrol eksekusi yang lebih detail:

### Basic Step Structure
```yaml
steps:
  - name: "build-app"
    type: "command"
    commands: ["npm run build"]
    description: "Build the application"
```

### Advanced Step Configuration
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

### Step Configuration Fields

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

### Silent Mode
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

### Output Saving & Context Variables
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

## File Template Rendering

Step `file_transfer` dengan `template: "enabled"` akan merender file dengan variable substitution sebelum upload:

```yaml
jobs:
  - name: deploy
    steps:
      - name: upload_config
        type: file_transfer
        source: "config.php"
        destination: "/var/www/config.php"
        template: "enabled"
```

### Transfer per-file dengan `files` (direkomendasikan untuk banyak file)

Saat Anda perlu mengunggah atau mengunduh beberapa file dengan target yang berbeda, gunakan array `files` di dalam langkah bertipe `file_transfer`. Setiap entri dapat memiliki `source` sendiri, `destination` opsional, dan `template` opsional sebagai override. Jika `files` ada, maka ia memiliki prioritas atas `source`/`sources` lama.

Contoh:

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

Perilaku:
- `destination` per-file akan menimpa `destination` pada level step. Jika keduanya tidak diatur untuk sebuah entri file, step akan menghasilkan error.
- Pola glob di `source` diekspansi secara lokal untuk operasi unggah; tiap hasil pencocokan akan ditransfer dengan mempertahankan path relatifnya di bawah direktori tujuan.
- `template` per-file akan menimpa pengaturan `template` pada level step.
- Jika beberapa file dispesifikasikan, `destination` harus berupa direktori (mengakhiri dengan `/` atau sudah ada sebagai direktori) — jika tidak, step akan error.
- Kompatibilitas mundur: bila `files` tidak disediakan, `source` / `sources` akan berfungsi seperti sebelumnya.


**File template (config.php):**
```php
<?php
$db_host = "{{db_host}}";
$db_name = "{{db_name}}";
```

**Execution variables:**
```yaml
executions:
  - name: "Deploy Prod"
    variables:
      db_host: "prod-db.example.com"
      db_name: "myapp_prod"
```

**Hasil:** File ter-render dengan nilai aktual sebelum di-upload.

## Pipeline Logging

Setiap eksekusi pipeline otomatis mencatat semua output ke file log untuk debugging dan audit trail.

### Log File Location

```
.sync_temp/
  └── logs/
      ├── watcher.log                           # Application/watcher logs
      └── {pipeline-name}-{timestamp}.log       # Pipeline execution logs
```

### Log Format

Log file berisi:
- **Pipeline info**: Nama pipeline dan timestamp eksekusi
- **Job execution**: Visual markers untuk setiap job
- **Command output**: Semua stdout/stderr dengan timestamp
- **Clean format**: ANSI escape codes di-strip otomatis

**Contoh log file (`split-database-2025-10-15-08-32-15.log`):**
```log
=== Pipeline: split-database ===
Started: 2025-10-15-08-32-15

[2025-10-15 08:32:15] ▶️  EXECUTING JOB: prepare (HEAD)
[2025-10-15 08:32:16] Docker version 20.10.7, build f0df350
[2025-10-15 08:32:16] 2025/10/15 08:32:16 DEBUG: Processed 100 rows from select query
[2025-10-15 08:32:16] 2025/10/15 08:32:16 DEBUG: Executing batch insert with 100 rows...
[2025-10-15 08:32:17] /home/user/app/internal/database/migration.go:164
[2025-10-15 08:32:17] [1.568ms] [rows:0] INSERT INTO `mockup_user_document` ...
```

### Error evidence (history buffer)

Selama eksekusi, make-sync menyimpan ringkasan output terbaru dalam buffer in-memory berkapasitas maksimum 300 KB (307200 bytes). Buffer ini bersifat FIFO: ketika total ukuran semua baris melebihi 300 KB, baris tertua akan di-evict sampai total ukuran kembali di bawah batas.

Saat terjadi kegagalan step (mis. timeout, error eksekusi atau session error), isi buffer ini akan "dikosongkan" (flushed) ke file log pipeline sebagai bukti (evidence). Dengan demikian bukti yang ditulis bukan lagi sekadar sejumlah baris tetap (mis. 10 baris), melainkan seluruh isi buffer sampai batas 300 KB — sehingga Anda mendapatkan konteks keluaran terbaru sebelum error.

Catatan penting:
- Kapasitas buffer default: 300 KB (307200 bytes).
- Eviksi FIFO: hanya terjadi jika total ukuran buffer melebihi 300 KB.
- Baris tunggal yang lebih besar dari 300 KB tidak otomatis dipotong; implementasi menyimpan baris tersebut (mungkin setelah mengusir semua baris lama), sehingga totalBytes bisa sementara melebihi cap untuk menahan baris utuh.
- Buffer bersifat per-eksekusi pipeline (tidak dipakai lintas eksekusi).

Header yang ditulis ke log akan menjelaskan bahwa ini adalah error evidence dan menunjukkan bahwa isi buffer (hingga 300 KB) disertakan. Contoh (format timestamp disertakan oleh sistem logging):

```log
[2025-10-15 12:37:04] === ERROR EVIDENCE (buffer up to 300KB) ===
[2025-10-15 12:37:04] <captured output line 1>
[2025-10-15 12:37:04] <captured output line 2>
... (hingga keseluruhan isi buffer)
```

### Controlling real-time logging with `log_output`

Gunakan field `log_output` untuk mengontrol apakah output real-time dari sebuah step ditulis ke file log pipeline. Field ini bersifat opt-in dan diselesaikan dengan prioritas berikut (tinggi → rendah):

- Step-level `log_output`
- Job-level `log_output`
- Pipeline-level `log_output`

Contoh: jika sebuah Step memiliki `log_output: true`, maka output Step tersebut akan ditulis ke file log walaupun Job atau Pipeline menyetel `log_output: false`. Kebalikan juga berlaku: `log_output: false` di level Step akan menonaktifkan logging untuk Step itu walaupun level Job/Pipeline mengizinkan.

Poin penting:
- Buffer in-memory 300 KB selalu meng-capture output, terlepas dari nilai `log_output`. Ini memungkinkan sistem menulis "error evidence" saat terjadi kegagalan meskipun file logging dinonaktifkan untuk operasi normal.
- `log_output` hanya mengontrol penulisan output ke file log dan tampilan real-time selama eksekusi. Ia tidak mengubah perilaku `save_output` yang menyimpan output ke variable konteks.

Contoh YAML singkat:

Step-level:

```yaml
steps:
  - name: "verbose-step"
    type: "command"
    commands: ["make test"]
    log_output: true
```

Job-level:

```yaml
jobs:
  - name: "build"
    log_output: true
    steps:
      - name: "run-tests"
        type: "command"
        commands: ["npm test"]
```

Pipeline-level:

```yaml
pipeline:
  name: "ci"
  log_output: false  # disable file logging by default; enable selectively via job/step
```

Gunakan `log_output` untuk mengurangi kebisingan log pada perintah yang sangat verbose, sambil tetap mengandalkan buffer bukti-kesalahan in-memory untuk debugging saat terjadi kegagalan.

### Use Cases

**Debugging**:
```bash
# Review output after execution
tail -f .sync_temp/logs/my-pipeline-2025-10-15-08-30-00.log

# Search for errors
grep -i "error" .sync_temp/logs/my-pipeline-*.log

# Check specific timestamp
grep "08:32:1" .sync_temp/logs/split-database-*.log
```

**Audit Trail**:
- Track semua command yang dijalankan
- Timestamp setiap operasi
- Archive logs untuk compliance

**CI/CD Integration**:
```bash
# Upload logs ke artifact storage
aws s3 cp .sync_temp/logs/ s3://my-bucket/pipeline-logs/ --recursive

# Attach logs ke notification
curl -X POST https://slack.com/api/files.upload \
  -F file=@.sync_temp/logs/deploy-prod-*.log \
  -F channels=deployments
```

**Troubleshooting**:
- Review output command yang gagal
- Compare logs dari different runs
- Debug timing issues dengan timestamp

### Log Features

- **Automatic**: No configuration required
- **Timestamped**: Setiap baris dengan `[YYYY-MM-DD HH:MM:SS]` format
- **Clean**: ANSI escape codes di-strip untuk readability
- **Comprehensive**: Capture semua stdout/stderr output
- **Persistent**: Tidak hilang setelah pipeline selesai

## Menjalankan Pipeline

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

## Real-time Output Streaming

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

### Silent Mode untuk Output Bersih
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

## Variable System

Pipeline mendukung 3 cara untuk mengelola variables:

### 1. Variables File (vars.yaml)

Sistem file-based untuk mengelola variables per environment:

```yaml
# File: .sync_pipelines/vars.yaml
dev:
  DOCKER_TAG: "dev-$(date +%Y%m%d-%H%M%S)"
  BUILD_ENV: "development"
  DEBUG: "true"

staging:
  DOCKER_TAG: "staging-latest"
  BUILD_ENV: "staging"

prod:
  DOCKER_TAG: "$(git rev-parse --short HEAD)"
  BUILD_ENV: "production"
```

```yaml
# Di make-sync.yaml
executions:
  - name: "Development Build"
    key: "dev"
    pipeline: "my-app.yaml"
    var: "dev"                    # Reference ke vars.yaml
    hosts: ["localhost"]
```

### 2. Direct Variables

Variables langsung di executions (fitur baru):

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

### 3. Hybrid Approach

Kombinasi vars.yaml + direct variables:

```yaml
executions:
  - name: "Development Build"
    key: "dev"
    pipeline: "my-app.yaml"
    var: "dev"                    # Base dari vars.yaml
    variables:                    # Override specific values
      DOCKER_TAG: "custom-override"
    hosts: ["localhost"]
```

## Variable Priority

Variables di-merge dengan priority sebagai berikut:

1. **CLI Overrides** (`--var KEY=value`) - **Tertinggi**
2. **execution.variables** (direct YAML) - **Tinggi**
3. **vars.yaml** (via execution.var) - **Sedang**
4. **Global pipeline.variables** - **Rendah**
5. **Runtime variables** (save_output) - **Terendah**

## Variable Interpolation

Gunakan format `{{VAR_NAME}}` di commands:

```yaml
steps:
  - name: "build-image"
    type: "command"
    commands: ["docker build -t {{DOCKER_REGISTRY}}/{{DOCKER_REPO}}:{{DOCKER_TAG}} ."]
    save_output: "image_id"

  - name: "deploy"
    type: "command" 
    commands: ["echo 'Deploying image {{image_id}}'"]
```

### Fields Supporting Variable Interpolation

Variable interpolation dengan format `{{VAR_NAME}}` didukung di field-field berikut:

| Field | Type | Description |
|-------|------|-------------|
| `commands` | []string | Command yang akan dieksekusi |
| `file` | string | Path file script (untuk `script` type) |
| `source` | string | Source path (untuk `file_transfer` type) |
| `destination` | string | Destination path (untuk `file_transfer` type) |
| `working_dir` | string | Working directory untuk step |
| `conditions[].pattern` | string | Regex pattern untuk conditional matching |
| `expect[].response` | string | Response untuk interactive prompts |

Contoh penggunaan di berbagai field:

```yaml
steps:
  - name: "run-script"
    type: "script"
    file: "scripts/{{SCRIPT_NAME}}.sh"  # Variable di file path
    working_dir: "{{WORKSPACE_PATH}}"   # Variable di working directory

  - name: "upload-config"
    type: "file_transfer"
    source: "config/{{ENV}}.yaml"       # Variable di source path
    destination: "/etc/app/{{ENV}}.yaml" # Variable di destination path
```

## Advanced Features

### Variable Persistence
```yaml
steps:
  - name: "get-version"
    commands: ["echo 'v1.2.3'"]
    save_output: "app_version"

  - name: "deploy"
    commands: ["echo 'Deploying {{app_version}}'"]
```

### Subroutine Calls
```yaml
steps:
  - name: "check-condition"
    commands: ["echo 'SUCCESS'"]
    conditions:
      - pattern: "SUCCESS"
        action: "goto_job"
        job: "success_handler"
```

### Pilihan action pada conditions

Field `action` menentukan tindakan yang dijalankan ketika sebuah `pattern` cocok dengan output command. Pilihan yang didukung:

- `continue`: Lanjut ke step berikutnya dalam job yang sama.
- `drop`: Hentikan eksekusi job (step berikutnya tidak dijalankan, job dianggap selesai tanpa error).
- `goto_step`: Lompat ke step lain di job yang sama (gunakan field `step` untuk target).
- `goto_job`: Lompat ke job lain di pipeline (gunakan field `job` untuk target).
- `fail`: Tandai job sebagai gagal.

Contoh penggunaan:

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

Penjelasan singkat:
- Gunakan `continue` untuk flow normal.
- Gunakan `drop` untuk membatalkan job saat kondisi tidak diinginkan terpenuhi.
- Gunakan `goto_step` / `goto_job` untuk branching, loop, atau penanganan error terstruktur.
- Gunakan `fail` untuk memicu fallback atau rollback otomatis.

### Else Condition
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
    else_action: "goto_job"
    else_job: "handle-unknown"
```

**Field Else:**
- `else_action`: Action jika tidak ada conditions yang match
- `else_step`: Target step untuk `goto_step`
- `else_job`: Target job untuk `goto_job`

## Konfigurasi

Pipeline disimpan di direktori yang dikonfigurasi:

```yaml
# Di make-sync.yaml
direct_access:
  pipeline_dir: ".sync_pipelines"  # Default location
```

**⚠️ Reserved Names**: Jangan gunakan nama `vars` atau `scripts` untuk pipeline.