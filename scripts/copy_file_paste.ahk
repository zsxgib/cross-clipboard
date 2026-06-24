; AutoHotkey v2 脚本：粘贴（Ctrl+V）
; 参数: 目标文件夹路径 (可省略, 默认 Win 端 test 目录)
; 写日志到 C:\Users\Administrator\ccb\ahk-paste.log
#SingleInstance Force

logFile := "C:\Users\Administrator\ccb\ahk-paste.log"
FileAppend "START`n", logFile

defaultTarget := "C:\Users\Administrator\Desktop\usb-2026-5-2\WDF\个人论文\已发表\专利\test"
target := (A_Args.Length >= 1) ? A_Args[1] : defaultTarget
FileAppend "target=" . target . "`n", logFile

; 打开目标文件夹
FileAppend "[1] Run explorer`n", logFile
Run 'explorer.exe "' . target . '"'

; 等 explorer 起来
Loop {
    wins := WinGetList("ahk_exe explorer.exe")
    if wins.Length >= 3 {
        Break
    }
    Sleep 100
}
FileAppend "  found " . wins.Length . " explorer windows`n", logFile

; 找到 CabinetWClass 窗口 + activate + click file list
FileAppend "[2] Find CabinetWClass`n", logFile
activated := false
for hwnd in wins {
    cls := WinGetClass("ahk_id " . hwnd)
    if cls = "CabinetWClass" {
        WinActivate "ahk_id " . hwnd
        WinGetPos &x, &y, &w, &h, "ahk_id " . hwnd
        FileAppend "  activate hwnd=" . hwnd . " size=" . w . "x" . h . "`n", logFile
        ; 点 file list 区域, 不是侧栏
        MouseClick "L", x + 200, y + 200
        activated := true
        Break
    }
}
if not activated {
    FileAppend "[2] FAIL no CabinetWClass`n", logFile
    Exit 1
}

; 等 explorer 焦点真正进入 file list
Sleep 1500

; Ctrl+V
FileAppend "[3] Ctrl+V`n", logFile
Send "^v"
Sleep 1500
FileAppend "[3] OK`n", logFile
FileAppend "END`n", logFile
