---
title: "Detail Perilaku"
---

## Detail Perilaku

- **Download Soft**: unduh file remote yang belum ada atau hash berbeda, dalam scope.
- **Download Force**: setelah unduh, hapus file lokal yang match scope namun tidak ada di DB remote.
- **Upload Soft**: unggah file lokal yang belum ada atau hash berbeda, dalam scope.
- **Upload Force**: tandai file lokal sebagai `checked`, lalu hapus file remote match scope yang tidak `checked`.
- **Scope “Sync Ignore !patterns”**: matcher dibuat dengan ignore all (`**`), lalu unignore per `!pattern`, `**/` varian, `.sync_temp` selalu dikecualikan.