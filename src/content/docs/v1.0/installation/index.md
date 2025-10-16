---
title: "Instalasi & Build"
order: 10
---

## Instalasi & Build

Panduan ini menjelaskan beberapa cara memasang make-sync sehingga dapat diakses secara global (`make-sync` di PATH) pada Linux, macOS, dan Windows. Secara umum Anda membutuhkan:

 - Go 1.24+ (untuk membangun dari sumber) jika ingin membuat binari sendiri.
 - Akses untuk menulis ke folder sistem (mis. `/usr/local/bin` atau `C:\\Program Files`) bila memilih pemasangan sistem-wide.

Pilih salah satu metode di bawah yang paling sesuai dengan kebutuhanmu: build lokal, `go install`, atau menaruh binari di folder PATH.

### 1) Build cepat (dari kode sumber lokal)
Jika kamu sedang berada di root repository (kode sumber sudah ada), jalankan:

```bash
go build -o make-sync ./...
```

Perintah di atas menghasilkan binari bernama `make-sync` (atau `make-sync.exe` di Windows) di folder sekarang. Untuk menjalankan dari lokasi manapun, pindahkan binari tersebut ke folder yang ada di PATH (lihat bagian per-OS).

### 2) `go install` (cara recommended untuk Go >=1.18)
Jika repository tersedia melalui VCS path (contoh: `github.com/rolldone/make-sync`), kamu bisa menginstal langsung ke GOBIN:

```bash
go install github.com/rolldone/make-sync@latest
```

Binary akan diinstall ke folder yang dikembalikan `go env GOBIN` (jika di-set) atau `$(go env GOPATH)/bin` (default: `$HOME/go/bin`). Pastikan folder tersebut ada di `PATH`.

```bash
# contoh menambah PATH (bash/zsh)
echo 'export PATH="$HOME/go/bin:$PATH"' >> ~/.profile
source ~/.profile
```

---

## Linux

Langkah singkat:

1. Build/install:

```bash
# build di direktori proyek
go build -o make-sync ./...

# atau install via go (jika tersedia sebagai modul publik)
go install github.com/rolldone/make-sync@latest
```

2. Pasang agar dapat diakses global (opsi):

- Pindahkan ke `/usr/local/bin` (system-wide):

```bash
sudo mv make-sync /usr/local/bin/
sudo chmod +x /usr/local/bin/make-sync
```

- Atau pakai GOBIN user-local (tanpa sudo):

```bash
mkdir -p "$HOME/.local/bin"
mv make-sync "$HOME/.local/bin/"
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.profile
source ~/.profile
```

3. Verifikasi:

```bash
make-sync --version
# atau
which make-sync
```

Catatan:
- Jika mendapat `permission denied`, jalankan `chmod +x` atau gunakan `sudo` untuk memindahkan ke direktori sistem.
- Jika `command not found`, periksa apakah folder bin ada di `PATH`.

---

## macOS

Langkah mirip Linux; perhatikan lokasi Homebrew pada Apple Silicon vs Intel.

1. Build/install:

```bash
go build -o make-sync ./...
# atau
go install github.com/rolldone/make-sync@latest
```

2. Pasang ke folder yang ada di PATH:

- Intel (default Homebrew path `/usr/local/bin`):

```bash
sudo mv make-sync /usr/local/bin/
sudo chmod +x /usr/local/bin/make-sync
```

- Apple Silicon (M1/M2) menggunakan Homebrew di `/opt/homebrew/bin`:

```bash
sudo mv make-sync /opt/homebrew/bin/
sudo chmod +x /opt/homebrew/bin/make-sync
```

3. Alternatif: Homebrew formula (jika tersedia)

Jika ada formula resmi, menginstal via Homebrew memberi integrasi lebih baik:

```bash
brew install rolldone/make-sync/make-sync
```

4. Verifikasi:

```bash
make-sync --version
which make-sync
```

---

## Windows

Di Windows ada beberapa opsi: menggunakan `go install` lalu menambahkan `GOBIN` ke PATH, atau memindahkan binari ke folder terdaftar PATH.

1. Build (PowerShell):

```powershell
# dari root project
go build -o make-sync.exe .\\...
# atau
go install github.com/rolldone/make-sync@latest
```

2. Pasang global:

- Buat folder install dan tambahkan ke PATH (eks. `C:\\Program Files\\make-sync`):

```powershell
New-Item -ItemType Directory -Path '"C:\\Program Files\\make-sync"' -Force
Move-Item -Path .\\make-sync.exe -Destination 'C:\\Program Files\\make-sync\\make-sync.exe'
# Tambah ke PATH (untuk user):
setx PATH "%PATH%;C:\\Program Files\\make-sync"
```

- Atau tambahkan `$(go env GOPATH)\\bin` ke PATH jika menggunakan `go install`.

3. Paket manager (opsional):

- Scoop (jika tersedia):

```powershell
scoop install make-sync
```

- Chocolatey (jika tersedia):

```powershell
choco install make-sync
```

4. Verifikasi (PowerShell):

```powershell
make-sync --version
where.exe make-sync
```

---

## Verifikasi & Troubleshooting

- Cek versi / help:

```bash
make-sync --version
make-sync --help
```

- `command not found`:
	- Pastikan folder bin (mis. `/usr/local/bin`, `$HOME/.local/bin`, atau `$GOPATH/bin`) ada di `PATH`.

- `permission denied`:
	- Tambahkan executable permission: `chmod +x make-sync` atau gunakan `sudo` saat memindahkan ke direktori sistem.

- `go: command not found`:
	- Install Go dan pastikan `go` di PATH. Lihat https://go.dev/doc/install

- SELinux/AppArmor (Linux):
	- Jika binary tidak dijalankan karena policy, cek audit logs dan set context/allow rule sesuai kebijakan sistem.

---

## Uninstall singkat

- Jika dipindahkan ke `/usr/local/bin` atau `/opt/homebrew/bin`:

```bash
sudo rm -f /usr/local/bin/make-sync
# atau
sudo rm -f /opt/homebrew/bin/make-sync
```

- Jika di `C:\\Program Files\\make-sync` (Windows): hapus folder dan update PATH.

---

Jika mau, saya bisa menambahkan langkah otomatis (skrip install) atau menulis instruksi Homebrew/Scoop/Chocolatey formula sebagai PR untuk mempermudah instalasi bagi pengguna lain.
- Jalankan seluruh test:

```bash
go test ./...
```