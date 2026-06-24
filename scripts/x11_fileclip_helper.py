#!/usr/bin/env python3
"""
x11_fileclip_helper.py - set X11 clipboard with multi-MIME file
payload using PyQt5. Stays alive inside Qt's event loop so
SelectionRequest events are answered (the actual X11 owner mechanism).

Usage: x11_fileclip_helper.py <path> [<path> ...]
Emits {"ok": true, "pid": <pid>} JSON to /tmp/x11_fileclip_helper.ok
once clipboard has been set. Stays alive until SIGTERM/SIGKILL.
"""
import json, os, sys
from PyQt5.QtGui import QGuiApplication
from PyQt5.QtCore import QMimeData, QUrl, QTimer

OKFILE = "/tmp/x11_fileclip_helper.ok"


def main() -> int:
    if len(sys.argv) < 2:
        with open(OKFILE, "w") as f:
            json.dump({"ok": False, "err": "usage: <path> [<path> ...]"}, f)
        return 2

    paths = [os.path.abspath(p) for p in sys.argv[1:]]
    for p in paths:
        if not os.path.exists(p):
            with open(OKFILE, "w") as f:
                json.dump({"ok": False, "err": f"not found: {p}"}, f)
            return 3

    app = QGuiApplication.instance() or QGuiApplication(sys.argv)
    cb = app.clipboard()

    md = QMimeData()
    gnome = "copy\n" + "\n".join(QUrl.fromLocalFile(p).toString() for p in paths) + "\n"
    md.setData("x-special/gnome-copied-files", gnome.encode("utf-8"))
    md.setUrls([QUrl.fromLocalFile(p) for p in paths])
    md.setText("\n".join(paths))

    cb.setMimeData(md, mode=cb.Clipboard)
    with open(OKFILE, "w") as f:
        json.dump({"ok": True, "paths": paths, "pid": os.getpid()}, f)

    # MUST run Qt event loop so SelectionRequest events are answered.
    # setQuitOnLastWindowClosed(False) keeps the loop alive even though
    # we have no visible windows.
    app.setQuitOnLastWindowClosed(False)
    return app.exec()


if __name__ == "__main__":
    sys.exit(main())
