; AutoHotkey v2 脚本：复制（Ctrl+C）
#SingleInstance Force

Log(msg) {
    FileAppend msg . "`n", "*"
}

Log("START")

; 打开文件夹并选中1.png
Log("[1] Run explorer")
Run 'explorer.exe /select,"C:\Users\Administrator\Desktop\usb-2026-5-2\WDF\个人论文\已发表\专利\test\1.png"'
Loop {
    wins := WinGetList("ahk_exe explorer.exe")
    if wins.Length >= 3 {
        Break
    }
    Sleep 50
}
Log("[1] OK")

; 找到CabinetWClass窗口并激活
Log("[2] Activate")
for hwnd in wins {
    cls := WinGetClass("ahk_id " . hwnd)
    if cls = "CabinetWClass" {
        WinActivate "ahk_id " . hwnd
        WinGetPos &x, &y, &w, &h, "ahk_id " . hwnd
        MouseClick "L", x + 100, y + 80
        Log("[2] OK")
        Break
    }
}

; Home定位到第一个文件
Log("[3] Home")
Send "{Home}"
Log("[3] OK")

; Ctrl+C复制
Log("[4] Ctrl+C")
Send "^c"
Log("[4] OK")

Log("END")
