# Windows service (ContextVerse)

Native SCM registration via:

```powershell
# elevated PowerShell
contextd init server --noui --non-interactive --server-dir C:\contextverse --admin admin
contextd server service install --server-dir C:\contextverse
contextd server service start
contextd server service stop
contextd server service uninstall
```

Service name: **ContextVerse**. Image path runs `contextd server start --server-dir … --open=false`.

Requires Administrator for install/uninstall/start/stop. On Linux/macOS the same CLI verbs print a Windows-only error — use [`contextd.service`](contextd.service) (systemd) or [`contextd.plist`](contextd.plist) (launchd) instead.
