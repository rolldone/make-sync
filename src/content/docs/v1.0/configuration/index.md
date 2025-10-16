---
title: "Konfigurasi"
---

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
    private_key: /home/you/.ssh/id_rsa
    remote_path: /home/user/project
    local_path: .
  ignores:
    - .sync_temp
  manual_transfer:
    - src
    - assets/images
```