; AutoHotkey v2 脚本：粘贴（Ctrl+V）
#SingleInstance Force

Log(msg) {
    FileAppend msg . "`n", "*"
}

Log("START")

; 打开文件夹
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

; 找到CabinetWClass窗口
Log("[2] Find CabinetWClass")
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

; Ctrl+V粘贴
Log("[3] Ctrl+V")
Send "^v"
Log("[3] OK")

Log("END")
