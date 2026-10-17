//go:build windows

package main

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

//go:embed payload/AltVoiceInput.exe
var embeddedAppExe []byte

//go:embed payload/voice_vocabulary.json
var embeddedVocabJSON []byte

var (
	user32               = syscall.NewLazyDLL("user32.dll")
	advapi32             = syscall.NewLazyDLL("advapi32.dll")
	procSetProcessDPIAware = user32.NewProc("SetProcessDPIAware")
	procMessageBoxW      = user32.NewProc("MessageBoxW")
	procRegOpenKeyExW    = advapi32.NewProc("RegOpenKeyExW")
	procRegSetValueExW   = advapi32.NewProc("RegSetValueExW")
	procRegCloseKey      = advapi32.NewProc("RegCloseKey")
)

func utf16Ptr(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func showMessageBox(title, text string, style uintptr) {
	procMessageBoxW.Call(
		0,
		uintptr(unsafe.Pointer(utf16Ptr(text))),
		uintptr(unsafe.Pointer(utf16Ptr(title))),
		style,
	)
}

func registerAutoStart(exePath string) {
	const HKEY_CURRENT_USER = 0x80000001
	const KEY_WRITE = 0x20006
	const REG_SZ = 1
	var hKey uintptr
	ret, _, _ := procRegOpenKeyExW.Call(
		HKEY_CURRENT_USER,
		uintptr(unsafe.Pointer(utf16Ptr(`Software\Microsoft\Windows\CurrentVersion\Run`))),
		0,
		KEY_WRITE,
		uintptr(unsafe.Pointer(&hKey)),
	)
	if ret != 0 {
		return
	}
	defer procRegCloseKey.Call(hKey)

	quoted := fmt.Sprintf(`"%s"`, exePath)
	u16 := utf16.Encode([]rune(quoted + "\x00"))
	procRegSetValueExW.Call(
		hKey,
		uintptr(unsafe.Pointer(utf16Ptr("AltVoiceInput"))),
		0,
		REG_SZ,
		uintptr(unsafe.Pointer(&u16[0])),
		uintptr(len(u16)*2),
	)
}

func createShortcuts(targetExe, workDir string) {
	psScript := fmt.Sprintf(`
$WshShell = New-Object -ComObject WScript.Shell
$DesktopPath = [Environment]::GetFolderPath("Desktop")
$StartMenuPath = [Environment]::GetFolderPath("Programs")

foreach ($Dir in @($DesktopPath, $StartMenuPath)) {
    if ($Dir -and (Test-Path $Dir)) {
        $ShortcutFile = Join-Path $Dir "Alt 智慧語音輸入.lnk"
        $Shortcut = $WshShell.CreateShortcut($ShortcutFile)
        $Shortcut.TargetPath = '%s'
        $Shortcut.WorkingDirectory = '%s'
        $Shortcut.Description = "Alt 智慧語音輸入與潤飾 (單按 Alt 說話/潤飾，Esc 取消)"
        $Shortcut.Save()
    }
}
`, targetExe, workDir)

	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", psScript)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = cmd.Run()
}

func main() {
	procSetProcessDPIAware.Call()

	// 1. Stop any running instance so we can overwrite cleanly
	killCmd := exec.Command("taskkill.exe", "/F", "/IM", "AltVoiceInput.exe")
	killCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = killCmd.Run()
	time.Sleep(350 * time.Millisecond)

	// 2. Determine installation directory: %LOCALAPPDATA%\AltVoiceInput
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		home, _ := os.UserHomeDir()
		localAppData = filepath.Join(home, "AppData", "Local")
	}
	installDir := filepath.Join(localAppData, "AltVoiceInput")
	if err := os.MkdirAll(installDir, 0755); err != nil {
		showMessageBox("安裝失敗", fmt.Sprintf("無法建立安裝目錄：\n%s\n%v", installDir, err), 0x00000010)
		return
	}

	// 3. Write AltVoiceInput.exe
	targetExe := filepath.Join(installDir, "AltVoiceInput.exe")
	if err := os.WriteFile(targetExe, embeddedAppExe, 0755); err != nil {
		showMessageBox("安裝失敗", fmt.Sprintf("無法寫入主程式：\n%s\n請先關閉正在運行的 AltVoiceInput.exe 後重試。\n(%v)", targetExe, err), 0x00000010)
		return
	}

	// 4. Write voice_vocabulary.json (preserve if already exists)
	targetVocab := filepath.Join(installDir, "voice_vocabulary.json")
	if _, err := os.Stat(targetVocab); os.IsNotExist(err) {
		_ = os.WriteFile(targetVocab, embeddedVocabJSON, 0644)
	}

	// 5. Register Windows startup & create Desktop / Start Menu shortcuts
	registerAutoStart(targetExe)
	createShortcuts(targetExe, installDir)

	// 6. Launch AltVoiceInput.exe immediately
	startCmd := exec.Command(targetExe)
	startCmd.Dir = installDir
	_ = startCmd.Start()

	// 7. Show completion message (MB_OK | MB_ICONINFORMATION | MB_SETFOREGROUND = 0x00010040)
	msg := fmt.Sprintf(
		"🎉 Alt 智慧語音輸入與潤飾 (Windows 旗艦版) 已安裝並啟動！\n\n"+
			"📂 安裝目錄：%s\n"+
			"🚀 已自動開啟「Windows 開機自動啟動」並建立「桌面捷徑」。\n"+
			"💡 程式已常駐於畫面右下角系統列（System Tray）。\n\n"+
			"━━━━━━━━━━ 使用方式（與 Mac 100%% 同步） ━━━━━━━━━━\n"+
			"1️⃣ 【語音口述】：單按一下「Alt」開始說話（畫面正下方即時顯示動態音波；若聲音過小滿 1.5 秒立即閃爍橘框警示），說完再按一下「Alt」自動潤飾並貼上！\n"+
			"2️⃣ 【反白潤飾 + 自動學詞】：反白選取任何文字後單按一下「Alt」，AI 自動潤飾替換，並自動比對記憶您的專屬詞彙！\n"+
			"3️⃣ 【隨時取消】：錄音或處理期間按「Esc」立即取消。",
		installDir,
	)
	showMessageBox("✅ Alt 智慧語音輸入與潤飾 — 安裝成功", msg, 0x00010040)
}
