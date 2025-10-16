---
title: "Direct Access (SSH)"
---

## Konfigurasi Direct Access (SSH)

`make-sync` dapat menghasilkan file SSH config otomatis untuk akses langsung ke remote.

### RemoteCommand Berdasarkan Target OS

- **Windows**:
  ```yaml
  RemoteCommand: cmd /K cd =remotePath
  ```
- **Linux/Unix**:
  ```yaml
  RemoteCommand: cd =remotePath && bash -l
  ```

### Contoh

```yaml
direct_access:
  config_file: ""  # Kosongkan untuk generate otomatis ke .sync_temp/.ssh/config
  ssh_configs:
    - Host: my-server
      HostName: 192.168.1.100
      User: username
      Port: "22"
      RemoteCommand: cmd /K cd =remotePath  # Windows target
      RequestTty: force
      StrictHostKeyChecking: "no"
      ServerAliveInterval: "300"
      ServerAliveCountMax: "2"
  ssh_commands:
    - access_name: Connect to Server
      command: ssh -v my-server
```

Setelah `make-sync direct_access`, file config akan di `.sync_temp/.ssh/config`.