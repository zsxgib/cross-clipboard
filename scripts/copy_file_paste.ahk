; AutoHotkey v2 脚本：粘贴（Ctrl+V）
; 参数: 目标文件夹路径
#SingleInstance Force

if (A_Args.Length < 1) {
    MsgBox "请提供目标文件夹路径作为参数"
    Exit 1
}

folder := A_Args[1]
filePath := folder . "\temp_marker.txt"

Log(msg) {
    FileAppend msg . "`n", "*"
}

Log("START")
Log("目标文件夹: " . folder)

; 打开目标文件夹
Log("[1] Run explorer")
Run 'explorer.exe "' . folder . '"'
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
