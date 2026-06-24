#SingleInstance Force
logFile := "C:\Users\Administrator\ccb\ahk-paste-real.log"
FileAppend "START`n", logFile

target := "C:\Users\Administrator\Desktop\usb-2026-5-2\WDF\个人论文\已发表\专利\test"
FileAppend "target=" . target . "`n", logFile

; 1) Get current clipboard file count (just to log)
FileAppend "[0] checking clip...`n", logFile
sleep 500

; 2) Open target explorer
FileAppend "[1] Run explorer target`n", logFile
Run 'explorer.exe "' . target . '"'

; 3) Wait for explorer
Loop {
    wins := WinGetList("ahk_exe explorer.exe")
    if wins.Length >= 3 {
        Break
    }
    Sleep 100
}
FileAppend "  found " . wins.Length . " explorer windows`n", logFile

; 4) Find CabinetWClass
FileAppend "[2] Find CabinetWClass`n", logFile
activated := false
for hwnd in wins {
    cls := WinGetClass("ahk_id " . hwnd)
    if cls = "CabinetWClass" {
        WinActivate "ahk_id " . hwnd
        WinGetPos &x, &y, &w, &h, "ahk_id " . hwnd
        FileAppend "  activate hwnd=" . hwnd . " size=" . w . "x" . h . "`n", logFile
        MouseClick "L", x + 200, y + 200
        activated := true
        Break
    }
}
if not activated {
    FileAppend "[2] FAIL no CabinetWClass`n", logFile
    Exit 1
}

; 5) Wait for explorer to focus on file list
Sleep 1500

; 6) Ctrl+V
FileAppend "[3] Ctrl+V`n", logFile
Send "^v"
Sleep 1500
FileAppend "[3] OK`n", logFile
FileAppend "END`n", logFile
