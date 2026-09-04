# The app icon

`icon.ico` is the single source. It is:

- embedded into the binary by `icon.go`, written to `~/.goaiteam/icon.ico` on
  launch, and loaded from there for the window and taskbar;
- compiled into a Windows resource so Explorer, the shortcut and Alt-Tab show it
  on the `.exe` itself.

Regenerate the resource after changing the icon:

```bash
go run github.com/akavel/rsrc@latest \
  -ico internal/desktop/icon.ico -arch amd64 -o rsrc_windows_amd64.syso
```

`rsrc_windows_amd64.syso` is committed because it is a build input: the `_windows_amd64`
suffix means the Go toolchain links it only for that target and ignores it everywhere else.
