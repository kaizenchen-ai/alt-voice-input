//go:build windows

package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

// Build-time injected key pool (populated by build_windows.sh for private distribution; empty in public repo)
var buildTimeKeyPool string

const (
	AutoChunkSeconds = 150.0
	SampleRate       = 16000
	Channels         = 1
	BitsPerSample    = 16
	ChunkBytes       = 3200 // 100ms of 16kHz 16-bit mono PCM
	NumWaveBuffers   = 8

	WM_DESTROY       = 0x0002
	WM_PAINT         = 0x000F
	WM_CLOSE         = 0x0010
	WM_COMMAND       = 0x0111
	WM_TIMER         = 0x0113
	WM_USER          = 0x0400
	WM_TRAYICON      = WM_USER + 1
	WM_UPDATE_HUD    = WM_USER + 2
	WM_LBUTTONUP     = 0x0202
	WM_RBUTTONUP     = 0x0205
	WM_CONTEXTMENU   = 0x007B

	WS_POPUP         = 0x80000000
	WS_EX_DLGMODALFRAME = 0x00000001
	WS_EX_TOPMOST    = 0x00000008
	WS_EX_TRANSPARENT = 0x00000020
	WS_EX_TOOLWINDOW = 0x00000080
	WS_EX_LAYERED    = 0x00080000
	WS_EX_NOACTIVATE = 0x08000000

	LWA_ALPHA        = 0x00000002
	SW_HIDE          = 0
	SW_SHOWNOACTIVATE = 4

	SPI_GETWORKAREA  = 0x0030
	SM_CXSCREEN      = 0
	SM_CYSCREEN      = 1

	NIM_ADD          = 0x00000000
	NIM_MODIFY       = 0x00000001
	NIM_DELETE       = 0x00000002
	NIF_MESSAGE      = 0x00000001
	NIF_ICON         = 0x00000002
	NIF_TIP          = 0x00000004
	NIF_INFO         = 0x00000010

	TPM_RIGHTBUTTON  = 0x0002
	TPM_NONOTIFY     = 0x0080
	TPM_RETURNCMD    = 0x0100
	MF_STRING        = 0x00000000
	MF_CHECKED       = 0x00000008
	MF_SEPARATOR     = 0x00000800
	MF_GRAYED        = 0x00000001

	ID_TRAY_STATUS   = 1001
	ID_TRAY_TOGGLE   = 1002
	ID_TRAY_AUTOSTART = 1003
	ID_TRAY_VOCAB    = 1004
	ID_TRAY_ENV      = 1005
	ID_TRAY_LOG      = 1006
	ID_TRAY_EXIT     = 1007

	VK_BACK          = 0x08
	VK_TAB           = 0x09
	VK_RETURN        = 0x0D
	VK_SHIFT         = 0x10
	VK_CONTROL       = 0x11
	VK_MENU          = 0x12 // Alt
	VK_ESCAPE        = 0x1B
	VK_LSHIFT        = 0xA0
	VK_RSHIFT        = 0xA1
	VK_LCONTROL      = 0xA2
	VK_RCONTROL      = 0xA3
	VK_LMENU         = 0xA4 // Left Alt
	VK_RMENU         = 0xA5 // Right Alt
	VK_E8            = 0xE8 // Unassigned menu-mask key

	KEYEVENTF_KEYUP  = 0x0002
	CF_UNICODETEXT   = 13
	GMEM_MOVEABLE    = 0x0002

	WAVE_MAPPER      = 0xFFFFFFFF
	WAVE_FORMAT_PCM  = 1
	WHDR_DONE        = 0x00000001
	WHDR_PREPARED    = 0x00000002

	TRANSPARENT_BK   = 1
	DT_CENTER        = 0x00000001
	DT_VCENTER       = 0x00000004
	DT_SINGLELINE    = 0x00000020
	DT_NOPREFIX      = 0x00000800
	PS_SOLID         = 0
	NULL_BRUSH       = 5
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	winmm    = syscall.NewLazyDLL("winmm.dll")
	advapi32 = syscall.NewLazyDLL("advapi32.dll")

	procCreateMutexW              = kernel32.NewProc("CreateMutexW")
	procGetModuleHandleW          = kernel32.NewProc("GetModuleHandleW")
	procGlobalAlloc               = kernel32.NewProc("GlobalAlloc")
	procGlobalFree                = kernel32.NewProc("GlobalFree")
	procGlobalLock                = kernel32.NewProc("GlobalLock")
	procGlobalUnlock              = kernel32.NewProc("GlobalUnlock")
	procRtlMoveMemory             = kernel32.NewProc("RtlMoveMemory")

	procSetProcessDPIAware        = user32.NewProc("SetProcessDPIAware")
	procRegisterClassExW          = user32.NewProc("RegisterClassExW")
	procCreateWindowExW           = user32.NewProc("CreateWindowExW")
	procDefWindowProcW            = user32.NewProc("DefWindowProcW")
	procGetMessageW               = user32.NewProc("GetMessageW")
	procTranslateMessage          = user32.NewProc("TranslateMessage")
	procDispatchMessageW          = user32.NewProc("DispatchMessageW")
	procPostQuitMessage           = user32.NewProc("PostQuitMessage")
	procPostMessageW              = user32.NewProc("PostMessageW")
	procShowWindow                = user32.NewProc("ShowWindow")
	procUpdateWindow              = user32.NewProc("UpdateWindow")
	procInvalidateRect            = user32.NewProc("InvalidateRect")
	procSetWindowPos              = user32.NewProc("SetWindowPos")
	procSetLayeredWindowAttributes = user32.NewProc("SetLayeredWindowAttributes")
	procSetWindowRgn              = user32.NewProc("SetWindowRgn")
	procSetTimer                  = user32.NewProc("SetTimer")
	procKillTimer                 = user32.NewProc("KillTimer")
	procBeginPaint                = user32.NewProc("BeginPaint")
	procEndPaint                  = user32.NewProc("EndPaint")
	procDrawTextW                 = user32.NewProc("DrawTextW")
	procFillRect                  = user32.NewProc("FillRect")
	procSystemParametersInfoW     = user32.NewProc("SystemParametersInfoW")
	procGetSystemMetrics          = user32.NewProc("GetSystemMetrics")
	procGetAsyncKeyState          = user32.NewProc("GetAsyncKeyState")
	procKeybdEvent                = user32.NewProc("keybd_event")
	procOpenClipboard             = user32.NewProc("OpenClipboard")
	procCloseClipboard            = user32.NewProc("CloseClipboard")
	procEmptyClipboard            = user32.NewProc("EmptyClipboard")
	procGetClipboardData          = user32.NewProc("GetClipboardData")
	procSetClipboardData          = user32.NewProc("SetClipboardData")
	procCreatePopupMenu           = user32.NewProc("CreatePopupMenu")
	procAppendMenuW               = user32.NewProc("AppendMenuW")
	procTrackPopupMenu            = user32.NewProc("TrackPopupMenu")
	procDestroyMenu               = user32.NewProc("DestroyMenu")
	procGetCursorPos              = user32.NewProc("GetCursorPos")
	procSetForegroundWindow       = user32.NewProc("SetForegroundWindow")
	procLoadIconW                 = user32.NewProc("LoadIconW")
	procLoadCursorW               = user32.NewProc("LoadCursorW")
	procMessageBeep               = user32.NewProc("MessageBeep")

	procCreateRoundRectRgn        = gdi32.NewProc("CreateRoundRectRgn")
	procCreateSolidBrush          = gdi32.NewProc("CreateSolidBrush")
	procCreatePen                 = gdi32.NewProc("CreatePen")
	procCreateFontW               = gdi32.NewProc("CreateFontW")
	procSelectObject              = gdi32.NewProc("SelectObject")
	procDeleteObject              = gdi32.NewProc("DeleteObject")
	procSetBkMode                 = gdi32.NewProc("SetBkMode")
	procSetTextColor              = gdi32.NewProc("SetTextColor")
	procRoundRect                 = gdi32.NewProc("RoundRect")
	procGetStockObject            = gdi32.NewProc("GetStockObject")
	procCreateCompatibleDC        = gdi32.NewProc("CreateCompatibleDC")
	procCreateCompatibleBitmap    = gdi32.NewProc("CreateCompatibleBitmap")
	procBitBlt                    = gdi32.NewProc("BitBlt")
	procDeleteDC                  = gdi32.NewProc("DeleteDC")

	procShellNotifyIconW          = shell32.NewProc("Shell_NotifyIconW")

	procWaveInOpen                = winmm.NewProc("waveInOpen")
	procWaveInClose               = winmm.NewProc("waveInClose")
	procWaveInPrepareHeader       = winmm.NewProc("waveInPrepareHeader")
	procWaveInUnprepareHeader     = winmm.NewProc("waveInUnprepareHeader")
	procWaveInAddBuffer           = winmm.NewProc("waveInAddBuffer")
	procWaveInStart               = winmm.NewProc("waveInStart")
	procWaveInReset               = winmm.NewProc("waveInReset")
	procPlaySoundW                = winmm.NewProc("PlaySoundW")

	procRegOpenKeyExW             = advapi32.NewProc("RegOpenKeyExW")
	procRegSetValueExW            = advapi32.NewProc("RegSetValueExW")
	procRegDeleteValueW           = advapi32.NewProc("RegDeleteValueW")
	procRegQueryValueExW          = advapi32.NewProc("RegQueryValueExW")
	procRegCloseKey               = advapi32.NewProc("RegCloseKey")
)

type POINT struct {
	X, Y int32
}

type RECT struct {
	Left, Top, Right, Bottom int32
}

type WNDCLASSEXW struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type MSG struct {
	HWnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      POINT
}

type PAINTSTRUCT struct {
	Hdc         uintptr
	FErase      int32
	RcPaint     RECT
	FRestore    int32
	FIncUpdate  int32
	RgbReserved [32]byte
}

type NOTIFYICONDATAW struct {
	CbSize           uint32
	HWnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         [16]byte
	HBalloonIcon     uintptr
}

type WAVEFORMATEX struct {
	WFormatTag      uint16
	NChannels       uint16
	NSamplesPerSec  uint32
	NAvgBytesPerSec uint32
	NBlockAlign     uint16
	WBitsPerSample  uint16
	CbSize          uint16
}

type WAVEHDR struct {
	LpData          uintptr
	DwBufferLength  uint32
	DwBytesRecorded uint32
	DwUser          uintptr
	DwFlags         uint32
	DwLoops         uint32
	LpNext          uintptr
	Reserved        uintptr
}

var (
	spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	waveFrames    = []string{" ▂▃", "▂▃▄", "▃▄▅", "▄▅▆", "▅▆▇", "▆▇█", "▇█▆", "█▆▅", "▆▅▄", "▅▄▃", "▄▃▂", "▃▂ "}
	candidateModels = []string{"gemini-flash-lite-latest", "gemini-3.1-flash-lite", "gemini-3.5-flash-lite"}

	appDataDir   string
	logPath      string
	vocabPath    string
	snapshotPath string
	envPath      string
	apiKeys      []string
)

func utf16Ptr(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func rgb(r, g, b byte) uintptr {
	return uintptr(uint32(r) | (uint32(g) << 8) | (uint32(b) << 16))
}

func logMsg(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	ts := time.Now().Format("2006-01-02 15:04:05")
	line := fmt.Sprintf("[%s] %s\r\n", ts, msg)
	if logPath != "" {
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err == nil {
			_, _ = f.WriteString(line)
			_ = f.Close()
		}
	}
}

func playSystemSound(alias string) {
	// SND_ALIAS | SND_ASYNC | SND_NODEFAULT = 0x00010000 | 0x0001 | 0x0002 = 0x00010003
	go func() {
		procPlaySoundW.Call(uintptr(unsafe.Pointer(utf16Ptr(alias))), 0, 0x00010003)
	}()
}

// Vocabulary & History Management
type VocabConfig struct {
	Description  string            `json:"description"`
	Mappings     map[string]string `json:"mappings"`
	ContextHints []string          `json:"context_hints"`
}

type HistoryItem struct {
	Text      string  `json:"text"`
	Timestamp float64 `json:"timestamp"`
}

func defaultVocabConfig() VocabConfig {
	return VocabConfig{
		Description: "Alt Voice Input - 使用者個性化專屬詞庫與熱詞對照表",
		Mappings: map[string]string{
			"HY":        "AGY",
			"hy":        "AGY",
			"A G Y":     "AGY",
			"愛吉歪":       "AGY",
			"Cloud":     "Claude",
			"cloudy":    "Claude",
			"Cloudy":    "Claude",
			"克勞德":       "Claude",
			"code x":    "Codex",
			"Code x":    "Codex",
			"code-x":    "Codex",
			"Code-X":    "Codex",
			"codeX":     "Codex",
			"CodeX":     "Codex",
			"co decks":  "Codex",
			"co-decks":  "Codex",
			"codecs":    "Codex",
			"codec":     "Codex",
			"Codecs":    "Codex",
			"科代克斯":      "Codex",
			"扣代克斯":      "Codex",
			"科德克斯":      "Codex",
			"扣德克斯":      "Codex",
			"科戴克斯":      "Codex",
			"考代克斯":      "Codex",
			"口代克斯":      "Codex",
			"扣得斯":       "Codex",
			"科代克":       "Codex",
			"西西低":       "CCD",
			"CCT":       "CCD",
			"CCB":       "CCD",
			"荷米斯":       "Hermes",
			"愛馬仕":       "Hermes",
			"太普利斯":      "Typeless",
			"type list": "Typeless",
			"人家Drive":   "仁家Drive",
			"任家Drive":   "仁家Drive",
			"偷一刻":       "TOEIC",
			"筆記LM":      "NotebookLM",
		},
		ContextHints: []string{
			"Codex（OpenAI 幕僚長代理人 / CLI，發音常被誤聽為 Code X、codecs、codec、科代克斯、扣代克斯，請務必統一修正為「Codex」）",
			"AGY（Google Antigravity 總調度核心，發音常被誤聽為 HY、A-G-Y，請統一修正為「AGY」）",
			"Claude（Anthropic 旗艦模型，發音常被誤聽為 Cloud、cloudy、克勞德，請統一修正為「Claude」）",
			"CCD（Claude Code CLI 專用簡稱，發音常被誤聽為 CCT、西西低，請統一修正為「CCD」）",
			"Hermes（本地自動化調度代理人，發音常被誤聽為 愛馬仕、荷米斯，請統一修正為「Hermes」）",
			"Typeless（語音輸入工具，發音常被誤聽為 type list）",
			"仁家Drive（外接硬碟標籤 仁家Drive1T）",
			"TOEIC / 多益（英語檢定測驗）",
			"NotebookLM（Google 筆記與多模態研究工具）",
			"Alt / Option（鍵盤修飾鍵）",
		},
	}
}

func initPathsAndConfig() {
	exePath, _ := os.Executable()
	exeDir := filepath.Dir(exePath)
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		localAppData = exeDir
	}
	appDataDir = filepath.Join(localAppData, "AltVoiceInput")
	_ = os.MkdirAll(appDataDir, 0755)

	logPath = filepath.Join(appDataDir, "alt_voice_input.log")
	vocabPath = filepath.Join(appDataDir, "voice_vocabulary.json")
	snapshotPath = filepath.Join(appDataDir, "voice_history_snapshots.json")
	envPath = filepath.Join(appDataDir, ".env")

	// Initialize default vocabulary file if missing
	if _, err := os.Stat(vocabPath); os.IsNotExist(err) {
		// Check next to exe first
		exeVocab := filepath.Join(exeDir, "voice_vocabulary.json")
		if data, err := os.ReadFile(exeVocab); err == nil {
			_ = os.WriteFile(vocabPath, data, 0644)
		} else {
			cfg := defaultVocabConfig()
			b, _ := json.MarshalIndent(cfg, "", "  ")
			_ = os.WriteFile(vocabPath, b, 0644)
		}
	}

	// Initialize .env if missing
	if _, err := os.Stat(envPath); os.IsNotExist(err) {
		exeEnv := filepath.Join(exeDir, ".env")
		if data, err := os.ReadFile(exeEnv); err == nil {
			_ = os.WriteFile(envPath, data, 0600)
		} else if buildTimeKeyPool != "" {
			content := fmt.Sprintf("# Alt Voice Input Gemini API Configuration\r\nKEY_POOL=%s\r\n", buildTimeKeyPool)
			_ = os.WriteFile(envPath, []byte(content), 0600)
		} else {
			content := "# 請在下方填入您的 Gemini API Key (支援多組以逗號分隔)\r\nGEMINI_API_KEY=\r\n"
			_ = os.WriteFile(envPath, []byte(content), 0600)
		}
	}

	loadAPIKeys()
}

func loadAPIKeys() {
	var keys []string
	addKey := func(k string) {
		k = strings.TrimSpace(strings.Trim(k, `"'`))
		if k == "" {
			return
		}
		for _, existing := range keys {
			if existing == k {
				return
			}
		}
		keys = append(keys, k)
	}

	for _, envVar := range []string{"GOOGLE_API_KEY_FALLBACK_1", "GOOGLE_API_KEY", "GEMINI_API_KEY"} {
		addKey(os.Getenv(envVar))
	}

	exePath, _ := os.Executable()
	exeDir := filepath.Dir(exePath)
	homeDir, _ := os.UserHomeDir()
	candidateFiles := []string{
		envPath,
		filepath.Join(exeDir, ".env"),
		filepath.Join(homeDir, ".hermes", ".env"),
		filepath.Join(homeDir, ".env"),
	}

	for _, fpath := range candidateFiles {
		data, err := os.ReadFile(fpath)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			k := strings.TrimSpace(parts[0])
			v := strings.TrimSpace(strings.Trim(parts[1], `"'`))
			if k == "KEY_POOL" {
				for _, p := range strings.Split(v, ",") {
					addKey(p)
				}
			} else if k == "GOOGLE_API_KEY_FALLBACK_1" || k == "GOOGLE_API_KEY" || k == "GEMINI_API_KEY" {
				addKey(v)
			}
		}
	}

	if buildTimeKeyPool != "" {
		for _, p := range strings.Split(buildTimeKeyPool, ",") {
			addKey(p)
		}
	}
	apiKeys = keys
	logMsg("Loaded %d API key(s).", len(apiKeys))
}

func loadVocabulary() VocabConfig {
	cfg := defaultVocabConfig()
	data, err := os.ReadFile(vocabPath)
	if err == nil {
		var loaded VocabConfig
		if json.Unmarshal(data, &loaded) == nil {
			if len(loaded.Mappings) > 0 {
				cfg.Mappings = loaded.Mappings
			}
			if len(loaded.ContextHints) > 0 {
				cfg.ContextHints = loaded.ContextHints
			}
		}
	}
	return cfg
}

func getSystemPrompt() string {
	cfg := loadVocabulary()
	var hintsLines []string
	for _, h := range cfg.ContextHints {
		hintsLines = append(hintsLines, "   - "+h)
	}
	hintsText := strings.Join(hintsLines, "\n")
	return fmt.Sprintf(`你是一位高階商務與專業技術的文字速記潤飾專家。
使用者是一位在電腦上進行 AI 代理人開發（AGY/Claude/Codex/Hermes）、程式編修與英語教學的專業工程師與教師。
使用者剛才透過語音口述了一段話。你的唯一任務是將這段口語音訊轉換為流暢、高品質的繁體中文（台灣習慣）：
1. 【嚴格清除口語贅字】：徹底刪除所有口頭禪與無意義發語詞（如：呃、額、那個、就是說、然後呢、對啊、嗯、啊等贅詞結巴）。
2. 【智慧錯字修正常理】：根據上下文自動修正同音錯字（例如將「私藥的發包」修正為「次要的報表/排程」、「AZ」修正為「Alt」等合理用詞）。
3. 【使用者專屬詞庫與發音校正（最高優先級）】：
   使用者在說話時常提及特定專有名詞，發音可能受口音影響或被語音模型聽偏。請務必結合使用者的繁體中文工作場景，強制校正為正確專業詞彙：
%s
4. 【智慧項目條列排版】：若口述內容包含「多個並列事項、不同任務清單、執行步驟、或同類要點」，請主動排版為清晰條理的項目清單（依語意採用數字編號「1. 2. 3.」或項目符號「•」分行呈現），使其一目了然。
5. 【結構通順與標點符號】：陳述性敘述請重組為自然分段、標點正確的標準繁體中文。
6. 【輸出規範鐵則】：直接輸出修飾後的純文字內容，絕對嚴禁任何開場白、不要引號、不要任何「好的」、「這是整理後的內容」等廢話。
7. 【人聲最大化提取】：請竭盡全力辨識音訊中的人聲語意。即使背景有微弱雜音或微弱電流聲，也請自動過濾雜音並精準還原人聲說話內容。只有在整段音訊完全沒有任何人聲說話時，才回傳 [EMPTY]。`, hintsText)
}

var asciiAlphaRegex = regexp.MustCompile(`[a-zA-Z]`)

func applyVocabularyPostProcessing(text string) string {
	cfg := loadVocabulary()
	for wrong, right := range cfg.Mappings {
		if asciiAlphaRegex.MatchString(wrong) {
			re, err := regexp.Compile(`(?i)\b` + regexp.QuoteMeta(wrong) + `\b`)
			if err == nil {
				text = re.ReplaceAllString(text, right)
			}
		} else {
			text = strings.ReplaceAll(text, wrong, right)
		}
	}
	return text
}

func loadHistorySnapshots() []HistoryItem {
	data, err := os.ReadFile(snapshotPath)
	if err != nil {
		return nil
	}
	var items []HistoryItem
	_ = json.Unmarshal(data, &items)
	return items
}

func appendHistorySnapshot(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	items := loadHistorySnapshots()
	items = append(items, HistoryItem{
		Text:      text,
		Timestamp: float64(time.Now().UnixNano()) / 1e9,
	})
	if len(items) > 30 {
		items = items[len(items)-30:]
	}
	b, err := json.MarshalIndent(items, "", "  ")
	if err == nil {
		_ = os.WriteFile(snapshotPath, b, 0644)
	}
}

func saveLearnedVocabulary(newPairs map[string]string) {
	if len(newPairs) == 0 {
		return
	}
	cfg := loadVocabulary()
	if cfg.Mappings == nil {
		cfg.Mappings = make(map[string]string)
	}
	added := make(map[string]string)
	for wrong, right := range newPairs {
		wrong = strings.Trim(strings.TrimSpace(wrong), `"'`)
		right = strings.Trim(strings.TrimSpace(right), `"'`)
		if wrong != "" && right != "" && wrong != right {
			if cfg.Mappings[wrong] != right {
				cfg.Mappings[wrong] = right
				added[wrong] = right
			}
		}
	}
	if len(added) > 0 {
		b, err := json.MarshalIndent(cfg, "", "  ")
		if err == nil {
			_ = os.WriteFile(vocabPath, b, 0644)
			logMsg("🧠 [Auto-Learned Vocabulary Persisted]: %v", added)
		}
	}
}

func extractAndLearnVocabulary(origText, editedText string) {
	origText = strings.TrimSpace(origText)
	editedText = strings.TrimSpace(editedText)
	if origText == "" || editedText == "" || origText == editedText {
		return
	}
	go func() {
		prompt := fmt.Sprintf(`請對照以下兩段文字：
【原始語音辨識輸出】：
%s

【使用者手動修正後的正確文字】：
%s

請精準萃取出使用者所做的「專用單詞、口語諧音、同音錯字」修正對照。
規則：
1. 必須將兩邊相同的周邊文字剝離，只萃取最小單位的核心詞彙（例如萃取 { "半百": "反白", "輪示": "潤飾", "HY": "AGY" }）。
2. 不要包含整句或相同的前後文。
3. 嚴格輸出純 JSON 鍵值對（{ "誤聽詞": "正確詞" }），無 Markdown 標記，無其他說明。
若無任何特定詞彙修正，請回傳 {}。`, origText, editedText)

		ans, err := callGeminiRawText(prompt, 0.1, 8*time.Second)
		if err != nil || ans == "" {
			return
		}
		ans = strings.TrimSpace(ans)
		ans = strings.TrimPrefix(ans, "```json")
		ans = strings.TrimPrefix(ans, "```")
		ans = strings.TrimSuffix(ans, "```")
		ans = strings.TrimSpace(ans)
		var pairs map[string]string
		if json.Unmarshal([]byte(ans), &pairs) == nil && len(pairs) > 0 {
			saveLearnedVocabulary(pairs)
		}
	}()
}

// Gemini API Client
func callGeminiMultimodal(wavBytes []byte, elapsedSec float64) (string, error) {
	loadAPIKeys()
	if len(apiKeys) == 0 {
		return "", fmt.Errorf("未設定 Gemini API Key，請右鍵系統列圖示設定 .env")
	}

	b64Audio := base64.StdEncoding.EncodeToString(wavBytes)
	payload := map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"parts": []map[string]interface{}{
					{"text": getSystemPrompt()},
					{
						"inline_data": map[string]string{
							"mime_type": "audio/wav",
							"data":      b64Audio,
						},
					},
				},
			},
		},
		"generationConfig": map[string]interface{}{
			"temperature": 0.2,
			"topP":        0.95,
		},
	}
	bodyBytes, _ := json.Marshal(payload)

	timeoutSec := math.Max(15.0, math.Min(40.0, elapsedSec*1.5))
	client := &http.Client{Timeout: time.Duration(timeoutSec * float64(time.Second))}

	var lastErr error
	for _, key := range apiKeys {
		for _, model := range candidateModels {
			url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, key)
			t0 := time.Now()
			resp, err := client.Post(url, "application/json", bytes.NewReader(bodyBytes))
			if err != nil {
				lastErr = err
				logMsg("Gemini API %s network error: %v", model, err)
				continue
			}
			respBytes, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != 200 {
				lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
				logMsg("Gemini API %s HTTP %d: %s", model, resp.StatusCode, string(respBytes))
				continue
			}
			var parsed struct {
				Candidates []struct {
					Content struct {
						Parts []struct {
							Text string `json:"text"`
						} `json:"parts"`
					} `json:"content"`
				} `json:"candidates"`
			}
			if err := json.Unmarshal(respBytes, &parsed); err != nil {
				lastErr = err
				continue
			}
			dt := time.Since(t0).Seconds()
			if len(parsed.Candidates) > 0 && len(parsed.Candidates[0].Content.Parts) > 0 {
				ans := strings.TrimSpace(parsed.Candidates[0].Content.Parts[0].Text)
				logMsg("Gemini API (%s) returned in %.2fs", model, dt)
				return ans, nil
			}
			logMsg("Gemini API (%s) returned empty parts in %.2fs", model, dt)
			return "", nil
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("連線失敗")
	}
	return "", lastErr
}

func callGeminiRawText(prompt string, temp float64, timeout time.Duration) (string, error) {
	loadAPIKeys()
	if len(apiKeys) == 0 {
		return "", fmt.Errorf("未設定 API Key")
	}
	payload := map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"parts": []map[string]string{
					{"text": prompt},
				},
			},
		},
		"generationConfig": map[string]interface{}{
			"temperature": temp,
			"topP":        0.95,
		},
	}
	bodyBytes, _ := json.Marshal(payload)
	client := &http.Client{Timeout: timeout}

	var lastErr error
	for _, key := range apiKeys {
		for _, model := range candidateModels {
			url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, key)
			resp, err := client.Post(url, "application/json", bytes.NewReader(bodyBytes))
			if err != nil {
				lastErr = err
				continue
			}
			respBytes, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != 200 {
				lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
				continue
			}
			var parsed struct {
				Candidates []struct {
					Content struct {
						Parts []struct {
							Text string `json:"text"`
						} `json:"parts"`
					} `json:"content"`
				} `json:"candidates"`
			}
			if json.Unmarshal(respBytes, &parsed) == nil {
				if len(parsed.Candidates) > 0 && len(parsed.Candidates[0].Content.Parts) > 0 {
					return strings.TrimSpace(parsed.Candidates[0].Content.Parts[0].Text), nil
				}
			}
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("連線失敗")
	}
	return "", lastErr
}

func callGeminiTextPolish(rawText string) (string, error) {
	prompt := fmt.Sprintf("%s\n\n使用者透過鍵盤反白選取的待潤飾草稿如下：\n```text\n%s\n```\n請直接輸出潤飾排版後的繁體中文結果：", getSystemPrompt(), rawText)
	return callGeminiRawText(prompt, 0.2, 6*time.Second)
}

// Win32 Clipboard & Synthetic Key Input
func getClipboardText() string {
	r, _, _ := procOpenClipboard.Call(0)
	if r == 0 {
		return ""
	}
	defer procCloseClipboard.Call()

	hMem, _, _ := procGetClipboardData.Call(CF_UNICODETEXT)
	if hMem == 0 {
		return ""
	}
	ptr, _, _ := procGlobalLock.Call(hMem)
	if ptr == 0 {
		return ""
	}
	defer procGlobalUnlock.Call(hMem)

	// Read UTF-16 null-terminated string
	var u16 []uint16
	for i := uintptr(0); i < 1024*1024; i += 2 {
		ch := *(*uint16)(unsafe.Pointer(ptr + i))
		if ch == 0 {
			break
		}
		u16 = append(u16, ch)
	}
	return string(utf16.Decode(u16))
}

func setClipboardText(s string) bool {
	u16 := utf16.Encode([]rune(s + "\x00"))
	byteLen := uintptr(len(u16) * 2)

	r, _, _ := procOpenClipboard.Call(0)
	if r == 0 {
		time.Sleep(15 * time.Millisecond)
		r, _, _ = procOpenClipboard.Call(0)
		if r == 0 {
			return false
		}
	}
	defer procCloseClipboard.Call()

	procEmptyClipboard.Call()
	hMem, _, _ := procGlobalAlloc.Call(GMEM_MOVEABLE, byteLen)
	if hMem == 0 {
		return false
	}
	ptr, _, _ := procGlobalLock.Call(hMem)
	if ptr == 0 {
		procGlobalFree.Call(hMem)
		return false
	}
	procRtlMoveMemory.Call(ptr, uintptr(unsafe.Pointer(&u16[0])), byteLen)
	procGlobalUnlock.Call(hMem)

	res, _, _ := procSetClipboardData.Call(CF_UNICODETEXT, hMem)
	if res == 0 {
		procGlobalFree.Call(hMem)
		return false
	}
	return true
}

func sendCtrlKey(vk byte) {
	// Ensure Alt keys are released before sending Ctrl+C / Ctrl+V
	procKeybdEvent.Call(VK_MENU, 0, KEYEVENTF_KEYUP, 0)
	procKeybdEvent.Call(VK_LMENU, 0, KEYEVENTF_KEYUP, 0)
	procKeybdEvent.Call(VK_RMENU, 0, KEYEVENTF_KEYUP, 0)
	time.Sleep(15 * time.Millisecond)

	procKeybdEvent.Call(VK_CONTROL, 0, 0, 0)
	time.Sleep(10 * time.Millisecond)
	procKeybdEvent.Call(uintptr(vk), 0, 0, 0)
	time.Sleep(15 * time.Millisecond)
	procKeybdEvent.Call(uintptr(vk), 0, KEYEVENTF_KEYUP, 0)
	time.Sleep(10 * time.Millisecond)
	procKeybdEvent.Call(VK_CONTROL, 0, KEYEVENTF_KEYUP, 0)
}

// Windows Registry Auto-Start Helper
const runRegSubKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const runRegValueName = "AltVoiceInput"
const HKEY_CURRENT_USER = 0x80000001
const KEY_READ = 0x20019
const KEY_WRITE = 0x20006
const REG_SZ = 1

func isAutoStartEnabled() bool {
	var hKey uintptr
	ret, _, _ := procRegOpenKeyExW.Call(
		HKEY_CURRENT_USER,
		uintptr(unsafe.Pointer(utf16Ptr(runRegSubKey))),
		0,
		KEY_READ,
		uintptr(unsafe.Pointer(&hKey)),
	)
	if ret != 0 {
		return false
	}
	defer procRegCloseKey.Call(hKey)

	var dataType uint32
	var dataLen uint32
	ret, _, _ = procRegQueryValueExW.Call(
		hKey,
		uintptr(unsafe.Pointer(utf16Ptr(runRegValueName))),
		0,
		uintptr(unsafe.Pointer(&dataType)),
		0,
		uintptr(unsafe.Pointer(&dataLen)),
	)
	return ret == 0 && dataLen > 0
}

func setAutoStart(enable bool) {
	var hKey uintptr
	ret, _, _ := procRegOpenKeyExW.Call(
		HKEY_CURRENT_USER,
		uintptr(unsafe.Pointer(utf16Ptr(runRegSubKey))),
		0,
		KEY_WRITE,
		uintptr(unsafe.Pointer(&hKey)),
	)
	if ret != 0 {
		return
	}
	defer procRegCloseKey.Call(hKey)

	if enable {
		exePath, err := os.Executable()
		if err != nil {
			return
		}
		quoted := fmt.Sprintf(`"%s"`, exePath)
		u16 := utf16.Encode([]rune(quoted + "\x00"))
		procRegSetValueExW.Call(
			hKey,
			uintptr(unsafe.Pointer(utf16Ptr(runRegValueName))),
			0,
			REG_SZ,
			uintptr(unsafe.Pointer(&u16[0])),
			uintptr(len(u16)*2),
		)
	} else {
		procRegDeleteValueW.Call(
			hKey,
			uintptr(unsafe.Pointer(utf16Ptr(runRegValueName))),
		)
	}
}

// Biquad High-Pass Filter (2nd-order Butterworth at fc=220Hz, fs=16000Hz) + De-click
type AudioDSPFilter struct {
	b0, b1, b2 float64
	a1, a2     float64
	x1, x2     float64
	y1, y2     float64
	prevSample int16
}

func newAudioDSPFilter(fc, fs float64) *AudioDSPFilter {
	w0 := 2.0 * math.Pi * fc / fs
	cosW0 := math.Cos(w0)
	sinW0 := math.Sin(w0)
	alpha := sinW0 / math.Sqrt2 // Butterworth Q = 1/sqrt(2)

	a0 := 1.0 + alpha
	b0 := ((1.0 + cosW0) / 2.0) / a0
	b1 := (-(1.0 + cosW0)) / a0
	b2 := ((1.0 + cosW0) / 2.0) / a0
	a1 := (-2.0 * cosW0) / a0
	a2 := (1.0 - alpha) / a0

	return &AudioDSPFilter{
		b0: b0, b1: b1, b2: b2,
		a1: a1, a2: a2,
	}
}

func (f *AudioDSPFilter) ProcessChunk(rawBytes []byte) ([]byte, float64) {
	numSamples := len(rawBytes) / 2
	if numSamples == 0 {
		return rawBytes, 0
	}
	outBytes := make([]byte, len(rawBytes))
	var sumSq float64

	for i := 0; i < numSamples; i++ {
		s := int16(binary.LittleEndian.Uint16(rawBytes[i*2 : i*2+2]))

		// De-click impulse suppression
		diff := int32(s) - int32(f.prevSample)
		if diff > 18000 || diff < -18000 {
			s = f.prevSample
		}
		f.prevSample = s

		x := float64(s)
		y := f.b0*x + f.b1*f.x1 + f.b2*f.x2 - f.a1*f.y1 - f.a2*f.y2
		f.x2 = f.x1
		f.x1 = x
		f.y2 = f.y1
		f.y1 = y

		if y > 32767.0 {
			y = 32767.0
		} else if y < -32768.0 {
			y = -32768.0
		}

		outSample := int16(y)
		binary.LittleEndian.PutUint16(outBytes[i*2:i*2+2], uint16(outSample))
		sumSq += y * y
	}

	rms := math.Sqrt(sumSq / float64(numSamples))
	return outBytes, rms
}

func encodeWAV(pcmData []byte) []byte {
	dataLen := uint32(len(pcmData))
	totalLen := 36 + dataLen
	buf := new(bytes.Buffer)
	buf.Grow(int(44 + dataLen))

	buf.WriteString("RIFF")
	_ = binary.Write(buf, binary.LittleEndian, totalLen)
	buf.WriteString("WAVEfmt ")
	_ = binary.Write(buf, binary.LittleEndian, uint32(16))
	_ = binary.Write(buf, binary.LittleEndian, uint16(1)) // PCM
	_ = binary.Write(buf, binary.LittleEndian, uint16(Channels))
	_ = binary.Write(buf, binary.LittleEndian, uint32(SampleRate))
	_ = binary.Write(buf, binary.LittleEndian, uint32(SampleRate*Channels*(BitsPerSample/8)))
	_ = binary.Write(buf, binary.LittleEndian, uint16(Channels*(BitsPerSample/8)))
	_ = binary.Write(buf, binary.LittleEndian, uint16(BitsPerSample))
	buf.WriteString("data")
	_ = binary.Write(buf, binary.LittleEndian, dataLen)
	buf.Write(pcmData)
	return buf.Bytes()
}

// FloatingHUD & Application State
type FloatingHUD struct {
	mu              sync.Mutex
	hwnd            uintptr
	w, h            int32
	hudMode         string // IDLE, RECORDING, PROCESSING, SUCCESS, CANCEL
	generation      int
	animIdx         int
	currentRMS      float64
	isUnclear       bool
	recordStartTime time.Time
	procStartTime   time.Time
	segmentIdx      int
	displayText     string
	textColor       uintptr
	borderColor     uintptr
	borderWidth     int32
}

func NewFloatingHUD() *FloatingHUD {
	return &FloatingHUD{
		w:           460,
		h:           54,
		hudMode:     "IDLE",
		textColor:   rgb(255, 255, 255),
		borderColor: rgb(95, 100, 115),
		borderWidth: 1,
	}
}

func rmsToWave(rms float64) string {
	norm := math.Min(1.0, math.Max(0.0, (rms-200.0)/1400.0))
	bars := []string{" ", " ", "▂", "▃", "▄", "▅", "▆", "▇", "█"}
	cIdx := int(norm * 8)
	if cIdx > 8 {
		cIdx = 8
	}
	mIdx := int(norm * 6)
	if mIdx > 8 {
		mIdx = 8
	}
	oIdx := int(norm * 4)
	if oIdx > 8 {
		oIdx = 8
	}
	return fmt.Sprintf("%s%s%s%s%s", bars[oIdx], bars[mIdx], bars[cIdx], bars[mIdx], bars[oIdx])
}

func (h *FloatingHUD) refreshStringsLocked() {
	switch h.hudMode {
	case "RECORDING":
		elapsed := int(time.Since(h.recordStartTime).Seconds())
		mins := elapsed / 60
		secs := elapsed % 60
		if h.isUnclear {
			blink := "⚠️"
			if h.animIdx%8 >= 5 {
				blink = "  "
			}
			h.displayText = fmt.Sprintf("%s %02d:%02d 聲音太小/聽不清楚！請靠近麥克風", blink, mins, secs)
			h.textColor = rgb(255, 210, 50)
			h.borderColor = rgb(255, 165, 0)
			h.borderWidth = 3
		} else {
			var wave string
			if h.currentRMS > 0 {
				wave = rmsToWave(h.currentRMS)
			} else {
				wave = waveFrames[h.animIdx%len(waveFrames)]
			}
			if h.segmentIdx > 1 {
				h.displayText = fmt.Sprintf("🔴 %02d:%02d 正在聆聽 [第%d段] %s", mins, secs, h.segmentIdx, wave)
			} else {
				h.displayText = fmt.Sprintf("🔴 %02d:%02d 正在聆聽... %s", mins, secs, wave)
			}
			h.textColor = rgb(255, 255, 255)
			h.borderColor = rgb(95, 100, 115)
			h.borderWidth = 1
		}
	case "PROCESSING":
		dt := time.Since(h.procStartTime).Seconds()
		spin := spinnerFrames[h.animIdx%len(spinnerFrames)]
		h.displayText = fmt.Sprintf("✨ AI 智慧潤飾中 (%.1fs) %s", dt, spin)
		h.textColor = rgb(110, 210, 255)
		h.borderColor = rgb(70, 170, 240)
		h.borderWidth = 2
	}
}

func (h *FloatingHUD) ShowRecording(segIdx int) {
	h.mu.Lock()
	h.generation++
	h.hudMode = "RECORDING"
	h.recordStartTime = time.Now()
	h.segmentIdx = segIdx
	h.animIdx = 0
	h.currentRMS = 0
	h.isUnclear = false
	h.refreshStringsLocked()
	h.mu.Unlock()
	h.requestRepaint(true)
}

func (h *FloatingHUD) UpdateSegment(segIdx int) {
	h.mu.Lock()
	h.segmentIdx = segIdx
	h.refreshStringsLocked()
	h.mu.Unlock()
	h.requestRepaint(false)
}

func (h *FloatingHUD) UpdateAudioLevel(rms float64, unclear bool) {
	h.mu.Lock()
	if h.hudMode != "RECORDING" {
		h.mu.Unlock()
		return
	}
	h.currentRMS = rms
	h.isUnclear = unclear
	h.refreshStringsLocked()
	h.mu.Unlock()
	h.requestRepaint(false)
}

func (h *FloatingHUD) ShowProcessing() {
	h.mu.Lock()
	h.generation++
	h.hudMode = "PROCESSING"
	h.procStartTime = time.Now()
	h.animIdx = 0
	h.currentRMS = 0
	h.isUnclear = false
	h.refreshStringsLocked()
	h.mu.Unlock()
	h.requestRepaint(true)
}

func (h *FloatingHUD) ShowSuccess(msg string) {
	h.mu.Lock()
	h.generation++
	gen := h.generation
	h.hudMode = "SUCCESS"
	h.displayText = msg
	h.textColor = rgb(85, 235, 125)
	h.borderColor = rgb(65, 210, 105)
	h.borderWidth = 2
	h.mu.Unlock()
	h.requestRepaint(true)

	go func(expectedGen int) {
		time.Sleep(950 * time.Millisecond)
		h.HideIfGen(expectedGen)
	}(gen)
}

func (h *FloatingHUD) ShowCancel(msg string) {
	h.mu.Lock()
	h.generation++
	gen := h.generation
	h.hudMode = "CANCEL"
	h.displayText = msg
	h.textColor = rgb(255, 185, 75)
	h.borderColor = rgb(255, 145, 45)
	h.borderWidth = 2
	h.mu.Unlock()
	h.requestRepaint(true)

	go func(expectedGen int) {
		time.Sleep(1100 * time.Millisecond)
		h.HideIfGen(expectedGen)
	}(gen)
}

func (h *FloatingHUD) HideIfGen(expectedGen int) {
	h.mu.Lock()
	if h.generation != expectedGen || h.hudMode == "RECORDING" {
		h.mu.Unlock()
		return
	}
	h.hudMode = "IDLE"
	hwnd := h.hwnd
	h.mu.Unlock()
	if hwnd != 0 {
		procPostMessageW.Call(hwnd, WM_UPDATE_HUD, 0, 0)
	}
}

func (h *FloatingHUD) TickAnimation() {
	h.mu.Lock()
	if h.hudMode != "RECORDING" && h.hudMode != "PROCESSING" {
		h.mu.Unlock()
		return
	}
	h.animIdx++
	h.refreshStringsLocked()
	h.mu.Unlock()
	h.requestRepaint(false)
}

func (h *FloatingHUD) requestRepaint(show bool) {
	h.mu.Lock()
	hwnd := h.hwnd
	h.mu.Unlock()
	if hwnd != 0 {
		flag := uintptr(0)
		if show {
			flag = 1
		}
		procPostMessageW.Call(hwnd, WM_UPDATE_HUD, flag, 0)
	}
}

func (h *FloatingHUD) repositionBottomCenter() {
	if h.hwnd == 0 {
		return
	}
	var workArea RECT
	ret, _, _ := procSystemParametersInfoW.Call(SPI_GETWORKAREA, 0, uintptr(unsafe.Pointer(&workArea)), 0)
	var x, y int32
	if ret != 0 {
		screenW := workArea.Right - workArea.Left
		x = workArea.Left + (screenW-h.w)/2
		y = workArea.Bottom - h.h - 24
	} else {
		screenW, _, _ := procGetSystemMetrics.Call(SM_CXSCREEN)
		screenH, _, _ := procGetSystemMetrics.Call(SM_CYSCREEN)
		x = (int32(screenW) - h.w) / 2
		y = int32(screenH) - h.h - 80
	}
	// HWND_TOPMOST = -1, SWP_NOACTIVATE = 0x0010
	procSetWindowPos.Call(h.hwnd, ^uintptr(0), uintptr(x), uintptr(y), uintptr(h.w), uintptr(h.h), 0x0010)
}

// Voice Input Manager
type AltVoiceManager struct {
	mu             sync.Mutex
	hud            *FloatingHUD
	isRecording    bool
	stopRecChan    chan struct{}
	recDoneChan    chan []byte
	recTotalStart  time.Time
	segStartTime   time.Time
	segmentIdx     int

	sessionToken   int
	deliveryMu     sync.Mutex
	nextDeliverSeq int
	pendingResults map[int]string
	activeTasks    int

	pasteMu        sync.Mutex
	isPasting      bool
	lastToggleTime time.Time
}

func NewAltVoiceManager(hud *FloatingHUD) *AltVoiceManager {
	m := &AltVoiceManager{
		hud:            hud,
		nextDeliverSeq: 1,
		pendingResults: make(map[int]string),
	}
	go m.autoChunkWatchdog()
	return m
}

func (m *AltVoiceManager) IsPasting() bool {
	m.pasteMu.Lock()
	defer m.pasteMu.Unlock()
	return m.isPasting
}

func (m *AltVoiceManager) Toggle() {
	m.mu.Lock()
	if m.isRecording {
		m.stopRecordingAndProcessLocked()
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()

	go m.polishSelectionOrStartRecording()
}

func (m *AltVoiceManager) CancelSession() {
	m.mu.Lock()
	m.sessionToken++
	wasRec := m.isRecording
	stopCh := m.stopRecChan
	m.isRecording = false
	m.stopRecChan = nil
	m.mu.Unlock()

	m.deliveryMu.Lock()
	m.pendingResults = make(map[int]string)
	m.nextDeliverSeq = 1
	m.deliveryMu.Unlock()

	if wasRec && stopCh != nil {
		close(stopCh)
	}
	playSystemSound("SystemHand")
	m.hud.ShowCancel("🛑 語音輸入已取消")
	logMsg("🛑 Session cancelled by user.")
}

func (m *AltVoiceManager) polishSelectionOrStartRecording() {
	m.pasteMu.Lock()
	m.isPasting = true
	oldClip := getClipboardText()
	sentinel := fmt.Sprintf("__ALT_VOICE_SENTINEL_%d__", time.Now().UnixNano())
	setClipboardText(sentinel)
	time.Sleep(25 * time.Millisecond)

	sendCtrlKey('C')
	time.Sleep(95 * time.Millisecond)

	newClip := getClipboardText()
	m.isPasting = false
	m.pasteMu.Unlock()

	if newClip == sentinel || strings.TrimSpace(newClip) == "" {
		// Restore original clipboard and enter Scheme B (Voice Recording)
		m.pasteMu.Lock()
		m.isPasting = true
		setClipboardText(oldClip)
		m.isPasting = false
		m.pasteMu.Unlock()

		logMsg("🎤 [Scheme B] No text highlighted. Starting voice recording...")
		m.mu.Lock()
		if !m.isRecording {
			m.startRecordingLocked()
		}
		m.mu.Unlock()
		return
	}

	// Scheme A: Selected text found!
	logMsg("📝 [Scheme A] Highlighted text (%d chars) captured for polishing.", len(newClip))
	m.hud.ShowProcessing()

	// Auto-Learn check against recent history
	m.checkAutoLearn(newClip)

	polished, err := callGeminiTextPolish(newClip)
	if err != nil || strings.TrimSpace(polished) == "" {
		logMsg("❌ [Scheme A] Polish error: %v", err)
		playSystemSound("SystemHand")
		m.hud.ShowCancel("❌ 潤飾失敗，請重試")
		m.pasteMu.Lock()
		m.isPasting = true
		setClipboardText(newClip)
		m.isPasting = false
		m.pasteMu.Unlock()
		return
	}

	finalText := applyVocabularyPostProcessing(polished)
	m.pasteText(finalText)
	playSystemSound("SystemAsterisk")
	m.hud.ShowSuccess("✅ 潤飾完成，已自動替換！")
}

func (m *AltVoiceManager) checkAutoLearn(editedText string) {
	history := loadHistorySnapshots()
	if len(history) == 0 {
		return
	}
	now := float64(time.Now().UnixNano()) / 1e9
	editedRunes := make(map[rune]bool)
	for _, r := range editedText {
		editedRunes[r] = true
	}

	for i := len(history) - 1; i >= 0; i-- {
		item := history[i]
		if now-item.Timestamp < 900 {
			orig := strings.TrimSpace(item.Text)
			if orig != "" && orig != strings.TrimSpace(editedText) {
				origRunes := make(map[rune]bool)
				for _, r := range orig {
					origRunes[r] = true
				}
				inter := 0
				union := len(origRunes)
				for r := range editedRunes {
					if origRunes[r] {
						inter++
					} else {
						union++
					}
				}
				if union > 0 && float64(inter)/float64(union) > 0.30 {
					logMsg("🔍 [Auto-Learn] Detected user edit compared to recent recognition. Triggering vocabulary extraction...")
					extractAndLearnVocabulary(orig, editedText)
					break
				}
			}
		}
	}
}

func (m *AltVoiceManager) startRecordingLocked() {
	m.sessionToken++
	m.isRecording = true
	m.recTotalStart = time.Now()
	m.segStartTime = time.Now()
	m.segmentIdx = 1

	m.deliveryMu.Lock()
	m.pendingResults = make(map[int]string)
	m.nextDeliverSeq = 1
	m.deliveryMu.Unlock()

	stopCh := make(chan struct{})
	doneCh := make(chan []byte, 1)
	m.stopRecChan = stopCh
	m.recDoneChan = doneCh

	playSystemSound("SystemDefault")
	m.hud.ShowRecording(1)
	go m.winmmRecordWorker(stopCh, doneCh)
}

func (m *AltVoiceManager) stopRecordingAndProcessLocked() {
	m.isRecording = false
	segElapsed := time.Since(m.segStartTime).Seconds()
	stopCh := m.stopRecChan
	doneCh := m.recDoneChan
	token := m.sessionToken
	seq := m.segmentIdx
	m.stopRecChan = nil
	m.recDoneChan = nil

	if stopCh != nil {
		close(stopCh)
	}
	playSystemSound("SystemDefault")
	m.hud.ShowProcessing()

	m.activeTasks++
	go func() {
		var pcm []byte
		if doneCh != nil {
			pcm = <-doneCh
		}
		m.processAudioWorker(pcm, segElapsed, false, token, seq)
	}()
}

func (m *AltVoiceManager) autoChunkWatchdog() {
	for {
		time.Sleep(500 * time.Millisecond)
		m.mu.Lock()
		if m.isRecording {
			segElapsed := time.Since(m.segStartTime).Seconds()
			if segElapsed >= AutoChunkSeconds {
				m.rotateChunkLocked(segElapsed)
			}
		}
		m.mu.Unlock()
	}
}

func (m *AltVoiceManager) rotateChunkLocked(segElapsed float64) {
	oldStop := m.stopRecChan
	oldDone := m.recDoneChan
	oldSeq := m.segmentIdx
	token := m.sessionToken

	newStop := make(chan struct{})
	newDone := make(chan []byte, 1)
	m.stopRecChan = newStop
	m.recDoneChan = newDone
	m.segmentIdx++
	m.segStartTime = time.Now()

	go m.winmmRecordWorker(newStop, newDone)
	if oldStop != nil {
		close(oldStop)
	}
	m.hud.UpdateSegment(m.segmentIdx)

	m.activeTasks++
	go func() {
		var pcm []byte
		if oldDone != nil {
			pcm = <-oldDone
		}
		m.processAudioWorker(pcm, segElapsed, true, token, oldSeq)
	}()
}

func (m *AltVoiceManager) winmmRecordWorker(stopCh chan struct{}, doneCh chan []byte) {
	var hWaveIn uintptr
	wfx := WAVEFORMATEX{
		WFormatTag:      WAVE_FORMAT_PCM,
		NChannels:       Channels,
		NSamplesPerSec:  SampleRate,
		NAvgBytesPerSec: SampleRate * Channels * (BitsPerSample / 8),
		NBlockAlign:     Channels * (BitsPerSample / 8),
		WBitsPerSample:  BitsPerSample,
		CbSize:          0,
	}

	ret, _, _ := procWaveInOpen.Call(
		uintptr(unsafe.Pointer(&hWaveIn)),
		WAVE_MAPPER,
		uintptr(unsafe.Pointer(&wfx)),
		0,
		0,
		0, // CALLBACK_NULL
	)
	if ret != 0 || hWaveIn == 0 {
		logMsg("❌ waveInOpen failed with code %d", ret)
		m.mu.Lock()
		m.isRecording = false
		m.mu.Unlock()
		m.hud.ShowCancel("❌ 找不到麥克風設備")
		doneCh <- nil
		return
	}

	rawBufs := make([][]byte, NumWaveBuffers)
	headers := make([]WAVEHDR, NumWaveBuffers)
	for i := 0; i < NumWaveBuffers; i++ {
		rawBufs[i] = make([]byte, ChunkBytes)
		headers[i] = WAVEHDR{
			LpData:         uintptr(unsafe.Pointer(&rawBufs[i][0])),
			DwBufferLength: ChunkBytes,
		}
		procWaveInPrepareHeader.Call(hWaveIn, uintptr(unsafe.Pointer(&headers[i])), unsafe.Sizeof(headers[i]))
		procWaveInAddBuffer.Call(hWaveIn, uintptr(unsafe.Pointer(&headers[i])), unsafe.Sizeof(headers[i]))
	}

	procWaveInStart.Call(hWaveIn)

	dsp := newAudioDSPFilter(220.0, float64(SampleRate))
	var pcmAccumulator bytes.Buffer
	recStart := time.Now()
	lastVoiceTime := recStart
	nextBufIdx := 0
	noiseFloor := 100.0

	ticker := time.NewTicker(15 * time.Millisecond)
	defer ticker.Stop()

loop:
	for {
		select {
		case <-stopCh:
			break loop
		case <-ticker.C:
			for {
				hdr := &headers[nextBufIdx]
				if hdr.DwFlags&WHDR_DONE == 0 {
					break
				}
				recorded := int(hdr.DwBytesRecorded)
				if recorded > 0 && recorded <= ChunkBytes {
					cleanChunk, rms := dsp.ProcessChunk(rawBufs[nextBufIdx][:recorded])
					pcmAccumulator.Write(cleanChunk)

					// Slowly adapt baseline noise floor downward or slightly upward during quiet frames
					if rms < noiseFloor {
						noiseFloor = 0.8*noiseFloor + 0.2*rms
					} else if rms < noiseFloor*1.6 && noiseFloor < 220.0 {
						noiseFloor = 0.95*noiseFloor + 0.05*rms
					}
					voiceThreshold := math.Max(220.0, math.Min(480.0, noiseFloor*2.5))

					now := time.Now()
					if rms > voiceThreshold {
						lastVoiceTime = now
					}
					elapsedTotal := now.Sub(recStart).Seconds()
					silenceDur := now.Sub(lastVoiceTime).Seconds()
					shouldWarn := elapsedTotal >= 1.5 && silenceDur >= 1.5
					m.hud.UpdateAudioLevel(rms, shouldWarn)
				}
				hdr.DwBytesRecorded = 0
				hdr.DwFlags &^= WHDR_DONE
				procWaveInAddBuffer.Call(hWaveIn, uintptr(unsafe.Pointer(hdr)), unsafe.Sizeof(*hdr))
				nextBufIdx = (nextBufIdx + 1) % NumWaveBuffers
			}
		}
	}

	procWaveInReset.Call(hWaveIn)
	for i := 0; i < NumWaveBuffers; i++ {
		hdr := &headers[i]
		if hdr.DwBytesRecorded > 0 && int(hdr.DwBytesRecorded) <= ChunkBytes {
			cleanChunk, _ := dsp.ProcessChunk(rawBufs[i][:hdr.DwBytesRecorded])
			pcmAccumulator.Write(cleanChunk)
		}
		if hdr.DwFlags&WHDR_PREPARED != 0 {
			procWaveInUnprepareHeader.Call(hWaveIn, uintptr(unsafe.Pointer(hdr)), unsafe.Sizeof(*hdr))
		}
	}
	procWaveInClose.Call(hWaveIn)
	doneCh <- normalizePCM(pcmAccumulator.Bytes())
}

func normalizePCM(pcm []byte) []byte {
	n := len(pcm) / 2
	if n == 0 {
		return pcm
	}
	var maxPeak int32
	for i := 0; i < n; i++ {
		s := int32(int16(binary.LittleEndian.Uint16(pcm[i*2 : i*2+2])))
		if s < 0 {
			s = -s
		}
		if s > maxPeak {
			maxPeak = s
		}
	}
	// If speech is present but quiet (peak between 250 and 14000), boost up to target 18000 (max 6.0x gain)
	if maxPeak < 250 || maxPeak >= 14000 {
		return pcm
	}
	gain := 18000.0 / float64(maxPeak)
	if gain > 6.0 {
		gain = 6.0
	}
	out := make([]byte, len(pcm))
	for i := 0; i < n; i++ {
		s := float64(int16(binary.LittleEndian.Uint16(pcm[i*2 : i*2+2]))) * gain
		if s > 32767.0 {
			s = 32767.0
		} else if s < -32768.0 {
			s = -32768.0
		}
		binary.LittleEndian.PutUint16(out[i*2:i*2+2], uint16(int16(s)))
	}
	return out
}

func (m *AltVoiceManager) processAudioWorker(pcmData []byte, elapsed float64, isAutoChunk bool, token, seq int) {
	defer func() {
		m.mu.Lock()
		if m.activeTasks > 0 {
			m.activeTasks--
		}
		m.mu.Unlock()
	}()

	m.mu.Lock()
	curToken := m.sessionToken
	isRec := m.isRecording
	active := m.activeTasks
	m.mu.Unlock()

	if token != curToken {
		return
	}

	if len(pcmData) < 4000 || elapsed < 0.35 {
		m.advanceEmptySeq(token, seq)
		if !isRec && !isAutoChunk && active <= 1 {
			playSystemSound("SystemHand")
			m.hud.ShowCancel("⚠️ 說話時間過短")
		}
		return
	}

	wavBytes := encodeWAV(pcmData)
	text, err := callGeminiMultimodal(wavBytes, elapsed)

	m.mu.Lock()
	curToken = m.sessionToken
	isRec = m.isRecording
	active = m.activeTasks
	m.mu.Unlock()

	if token != curToken {
		return
	}

	if text != "" && text != "[EMPTY]" && strings.TrimSpace(text) != "" {
		rawSTT := strings.TrimSpace(text)
		if polished, pErr := callGeminiTextPolish(rawSTT); pErr == nil && strings.TrimSpace(polished) != "" {
			rawSTT = strings.TrimSpace(polished)
		}
		finalText := applyVocabularyPostProcessing(rawSTT)
		logMsg("✨ [AI Result Seq %d]: %s", seq, finalText)
		m.enqueueDelivery(token, seq, finalText)
	} else if err != nil {
		logMsg("❌ [AI Error Seq %d]: %v", seq, err)
		m.advanceEmptySeq(token, seq)
		if !isRec && !isAutoChunk && active <= 1 {
			playSystemSound("SystemHand")
			m.hud.ShowCancel("❌ " + err.Error())
		}
	} else {
		logMsg("⚠️ [AI Result Seq %d]: No speech recognized.", seq)
		m.advanceEmptySeq(token, seq)
		if !isRec && !isAutoChunk && active <= 1 {
			playSystemSound("SystemHand")
			m.hud.ShowCancel("⚠️ 未辨識出有效聲音")
		}
	}
}

func (m *AltVoiceManager) enqueueDelivery(token, seq int, text string) {
	m.deliveryMu.Lock()
	defer m.deliveryMu.Unlock()
	m.mu.Lock()
	curToken := m.sessionToken
	m.mu.Unlock()
	if token != curToken {
		return
	}
	m.pendingResults[seq] = text
	m.drainDeliveryLocked()
}

func (m *AltVoiceManager) advanceEmptySeq(token, seq int) {
	m.deliveryMu.Lock()
	defer m.deliveryMu.Unlock()
	m.mu.Lock()
	curToken := m.sessionToken
	m.mu.Unlock()
	if token != curToken {
		return
	}
	if m.nextDeliverSeq == seq {
		m.nextDeliverSeq++
		m.drainDeliveryLocked()
	}
}

func (m *AltVoiceManager) drainDeliveryLocked() {
	for {
		text, ok := m.pendingResults[m.nextDeliverSeq]
		if !ok {
			break
		}
		delete(m.pendingResults, m.nextDeliverSeq)
		m.pasteText(text)
		m.nextDeliverSeq++

		m.mu.Lock()
		isRec := m.isRecording
		m.mu.Unlock()
		if !isRec && len(m.pendingResults) == 0 {
			playSystemSound("SystemAsterisk")
			m.hud.ShowSuccess("✅ 潤飾完成，已自動貼上！")
		}
	}
}

func (m *AltVoiceManager) pasteText(text string) {
	m.pasteMu.Lock()
	defer m.pasteMu.Unlock()
	m.isPasting = true
	defer func() { m.isPasting = false }()

	setClipboardText(text)
	time.Sleep(35 * time.Millisecond)
	sendCtrlKey('V')
	time.Sleep(80 * time.Millisecond)
	appendHistorySnapshot(text)
}

// Global Option/Alt & Esc Polling Engine (with Menu-Bar Masking)
func startKeyboardMonitor(mgr *AltVoiceManager) {
	isKeyDown := func(vk int) bool {
		ret, _, _ := procGetAsyncKeyState.Call(uintptr(vk))
		return (ret & 0x8000) != 0
	}

	var altPressed bool
	var altPressTime time.Time
	var otherKeyPressed bool
	var menuMasked bool
	var escWasDown bool

	for {
		time.Sleep(10 * time.Millisecond)
		if mgr.IsPasting() {
			continue
		}

		// Esc instant cancel
		escDown := isKeyDown(VK_ESCAPE)
		if escDown && !escWasDown {
			mgr.mu.Lock()
			busy := mgr.isRecording || mgr.activeTasks > 0
			mgr.mu.Unlock()
			if busy {
				mgr.CancelSession()
			}
		}
		escWasDown = escDown

		altDown := isKeyDown(VK_MENU) || isKeyDown(VK_LMENU) || isKeyDown(VK_RMENU)
		if altDown {
			if !altPressed {
				altPressed = true
				altPressTime = time.Now()
				otherKeyPressed = false
				menuMasked = false
			} else {
				// Check if any non-Alt key was pressed while Alt is held
				for vk := 0x08; vk <= 0xFE; vk++ {
					if vk == VK_MENU || vk == VK_LMENU || vk == VK_RMENU || vk == VK_E8 {
						continue
					}
					if isKeyDown(vk) {
						otherKeyPressed = true
						break
					}
				}
				// Send unassigned vkE8 after 35ms of solo Alt hold to prevent Windows menu bar activation
				if !otherKeyPressed && !menuMasked && time.Since(altPressTime) >= 35*time.Millisecond {
					menuMasked = true
					procKeybdEvent.Call(VK_E8, 0, 0, 0)
					procKeybdEvent.Call(VK_E8, 0, KEYEVENTF_KEYUP, 0)
				}
			}
		} else if altPressed {
			dur := time.Since(altPressTime).Seconds()
			wasSolo := !otherKeyPressed
			altPressed = false

			if wasSolo && dur >= 0.02 && dur <= 1.2 {
				mgr.mu.Lock()
				sinceLast := time.Since(mgr.lastToggleTime).Seconds()
				if sinceLast >= 0.35 {
					mgr.lastToggleTime = time.Now()
					mgr.mu.Unlock()
					logMsg("🔘 Alt tapped (%.2fs). Toggling...", dur)
					mgr.Toggle()
				} else {
					mgr.mu.Unlock()
				}
			}
		}
	}
}

// Win32 Window Procedure & Tray Handling
var (
	globalHUD *FloatingHUD
	globalMgr *AltVoiceManager
	nid       NOTIFYICONDATAW
	hFontHUD  uintptr
)

func addTrayIcon(hwnd uintptr) {
	// IDI_APPLICATION = 32512
	hIcon, _, _ := procLoadIconW.Call(0, uintptr(32512))
	nid = NOTIFYICONDATAW{
		HWnd:             hwnd,
		UID:              1,
		UFlags:           NIF_MESSAGE | NIF_ICON | NIF_TIP | NIF_INFO,
		UCallbackMessage: WM_TRAYICON,
		HIcon:            hIcon,
	}
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	copy(nid.SzTip[:], utf16.Encode([]rune("Alt 智慧語音輸入與潤飾 (單按 Alt 說話/潤飾，Esc 取消)\x00")))
	copy(nid.SzInfoTitle[:], utf16.Encode([]rune("Alt 智慧語音輸入已啟動\x00")))
	copy(nid.SzInfo[:], utf16.Encode([]rune("單按一下 Alt 即可說話或潤飾反白文字，按 Esc 取消。\x00")))
	procShellNotifyIconW.Call(NIM_ADD, uintptr(unsafe.Pointer(&nid)))
}

func removeTrayIcon() {
	procShellNotifyIconW.Call(NIM_DELETE, uintptr(unsafe.Pointer(&nid)))
}

func showTrayMenu(hwnd uintptr) {
	hMenu, _, _ := procCreatePopupMenu.Call()
	if hMenu == 0 {
		return
	}
	defer procDestroyMenu.Call(hMenu)

	procAppendMenuW.Call(hMenu, MF_STRING|MF_GRAYED, ID_TRAY_STATUS, uintptr(unsafe.Pointer(utf16Ptr("✨ Alt 智慧語音輸入與潤飾 (待命中)"))))
	procAppendMenuW.Call(hMenu, MF_SEPARATOR, 0, 0)
	procAppendMenuW.Call(hMenu, MF_STRING, ID_TRAY_TOGGLE, uintptr(unsafe.Pointer(utf16Ptr("🎤 開始 / 停止錄音 (單按 Alt)"))))

	autoFlags := uintptr(MF_STRING)
	if isAutoStartEnabled() {
		autoFlags |= MF_CHECKED
	}
	procAppendMenuW.Call(hMenu, autoFlags, ID_TRAY_AUTOSTART, uintptr(unsafe.Pointer(utf16Ptr("🚀 開機自動啟動 (Windows 登入即常駐)"))))
	procAppendMenuW.Call(hMenu, MF_SEPARATOR, 0, 0)
	procAppendMenuW.Call(hMenu, MF_STRING, ID_TRAY_VOCAB, uintptr(unsafe.Pointer(utf16Ptr("📚 編輯個人專屬詞庫 (voice_vocabulary.json)"))))
	procAppendMenuW.Call(hMenu, MF_STRING, ID_TRAY_ENV, uintptr(unsafe.Pointer(utf16Ptr("🔑 設定 Gemini API Key (.env)"))))
	procAppendMenuW.Call(hMenu, MF_STRING, ID_TRAY_LOG, uintptr(unsafe.Pointer(utf16Ptr("📋 查看運行日誌 (alt_voice_input.log)"))))
	procAppendMenuW.Call(hMenu, MF_SEPARATOR, 0, 0)
	procAppendMenuW.Call(hMenu, MF_STRING, ID_TRAY_EXIT, uintptr(unsafe.Pointer(utf16Ptr("❌ 結束程式"))))

	var pt POINT
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForegroundWindow.Call(hwnd)
	cmd, _, _ := procTrackPopupMenu.Call(hMenu, TPM_RETURNCMD|TPM_NONOTIFY|TPM_RIGHTBUTTON, uintptr(pt.X), uintptr(pt.Y), 0, hwnd, 0)

	switch cmd {
	case ID_TRAY_TOGGLE:
		globalMgr.Toggle()
	case ID_TRAY_AUTOSTART:
		setAutoStart(!isAutoStartEnabled())
	case ID_TRAY_VOCAB:
		_ = exec.Command("notepad.exe", vocabPath).Start()
	case ID_TRAY_ENV:
		_ = exec.Command("notepad.exe", envPath).Start()
	case ID_TRAY_LOG:
		_ = exec.Command("notepad.exe", logPath).Start()
	case ID_TRAY_EXIT:
		removeTrayIcon()
		procPostQuitMessage.Call(0)
	}
}

func wndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case WM_TIMER:
		if globalHUD != nil {
			globalHUD.TickAnimation()
		}
		return 0

	case WM_UPDATE_HUD:
		if globalHUD != nil {
			globalHUD.mu.Lock()
			mode := globalHUD.hudMode
			globalHUD.mu.Unlock()
			if mode == "IDLE" {
				procShowWindow.Call(hwnd, SW_HIDE)
			} else {
				if wParam == 1 {
					globalHUD.repositionBottomCenter()
					procShowWindow.Call(hwnd, SW_SHOWNOACTIVATE)
				}
				procInvalidateRect.Call(hwnd, 0, 0)
			}
		}
		return 0

	case WM_PAINT:
		var ps PAINTSTRUCT
		hdc, _, _ := procBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		if hdc != 0 && globalHUD != nil {
			globalHUD.mu.Lock()
			w, h := globalHUD.w, globalHUD.h
			text := globalHUD.displayText
			txtColor := globalHUD.textColor
			bdrColor := globalHUD.borderColor
			bdrWidth := globalHUD.borderWidth
			globalHUD.mu.Unlock()

			memDC, _, _ := procCreateCompatibleDC.Call(hdc)
			memBmp, _, _ := procCreateCompatibleBitmap.Call(hdc, uintptr(w), uintptr(h))
			oldBmp, _, _ := procSelectObject.Call(memDC, memBmp)

			// Background fill
			bgBrush, _, _ := procCreateSolidBrush.Call(rgb(24, 24, 30))
			rc := RECT{0, 0, w, h}
			procFillRect.Call(memDC, uintptr(unsafe.Pointer(&rc)), bgBrush)

			// Rounded pill border
			pen, _, _ := procCreatePen.Call(PS_SOLID, uintptr(bdrWidth), bdrColor)
			oldPen, _, _ := procSelectObject.Call(memDC, pen)
			oldBrush, _, _ := procSelectObject.Call(memDC, bgBrush)
			procRoundRect.Call(memDC, 1, 1, uintptr(w-1), uintptr(h-1), 26, 26)
			procSelectObject.Call(memDC, oldBrush)
			procSelectObject.Call(memDC, oldPen)
			procDeleteObject.Call(pen)
			procDeleteObject.Call(bgBrush)

			// Text
			if hFontHUD != 0 {
				procSelectObject.Call(memDC, hFontHUD)
			}
			procSetBkMode.Call(memDC, TRANSPARENT_BK)
			procSetTextColor.Call(memDC, txtColor)
			textRc := RECT{12, 0, w - 12, h}
			procDrawTextW.Call(
				memDC,
				uintptr(unsafe.Pointer(utf16Ptr(text))),
				^uintptr(0), // -1 null-terminated
				uintptr(unsafe.Pointer(&textRc)),
				DT_CENTER|DT_VCENTER|DT_SINGLELINE|DT_NOPREFIX,
			)

			// Blit to screen (SRCCOPY = 0x00CC0020)
			procBitBlt.Call(hdc, 0, 0, uintptr(w), uintptr(h), memDC, 0, 0, 0x00CC0020)

			procSelectObject.Call(memDC, oldBmp)
			procDeleteObject.Call(memBmp)
			procDeleteDC.Call(memDC)
		}
		procEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		return 0

	case WM_TRAYICON:
		low := uint32(lParam & 0xFFFF)
		if low == WM_RBUTTONUP || low == WM_CONTEXTMENU {
			showTrayMenu(hwnd)
		} else if low == WM_LBUTTONUP {
			showTrayMenu(hwnd)
		}
		return 0

	case WM_DESTROY:
		removeTrayIcon()
		procPostQuitMessage.Call(0)
		return 0
	}

	ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return ret
}

func main() {
	procSetProcessDPIAware.Call()
	initPathsAndConfig()

	// Single instance check
	mutexName := utf16Ptr("Local\\AltVoiceInput_Singleton_Mutex")
	hMutex, _, errNo := procCreateMutexW.Call(0, 1, uintptr(unsafe.Pointer(mutexName)))
	if hMutex == 0 || errNo == syscall.ERROR_ALREADY_EXISTS {
		logMsg("Another instance is already running. Exiting.")
		return
	}

	logMsg("=== Starting Alt Voice Input (Windows Native Edition) ===")

	// Ensure auto-start is registered on first run
	if !isAutoStartEnabled() {
		setAutoStart(true)
	}

	hInst, _, _ := procGetModuleHandleW.Call(0)
	className := utf16Ptr("AltVoiceInputHUDClass")
	hCursor, _, _ := procLoadCursorW.Call(0, uintptr(32512)) // IDC_ARROW

	wc := WNDCLASSEXW{
		Style:         0x0003, // CS_HREDRAW | CS_VREDRAW
		LpfnWndProc:   syscall.NewCallback(wndProc),
		HInstance:     hInst,
		HCursor:       hCursor,
		LpszClassName: className,
	}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	globalHUD = NewFloatingHUD()
	globalMgr = NewAltVoiceManager(globalHUD)

	// Create Font: Microsoft JhengHei UI, 19px height (~11pt), weight 600 (SemiBold), CLEARTYPE_QUALITY (5)
	hFontHUD, _, _ = procCreateFontW.Call(
		uintptr(^uint32(19 - 1)), // -19
		0, 0, 0,
		600, // FW_SEMIBOLD
		0, 0, 0,
		1, // DEFAULT_CHARSET
		0, 0,
		5, // CLEARTYPE_QUALITY
		0,
		uintptr(unsafe.Pointer(utf16Ptr("Microsoft JhengHei UI"))),
	)

	exStyle := uintptr(WS_EX_LAYERED | WS_EX_TOPMOST | WS_EX_TOOLWINDOW | WS_EX_NOACTIVATE | WS_EX_TRANSPARENT)
	hwnd, _, _ := procCreateWindowExW.Call(
		exStyle,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(utf16Ptr("AltVoiceInputHUD"))),
		WS_POPUP,
		100, 100,
		uintptr(globalHUD.w), uintptr(globalHUD.h),
		0, 0, hInst, 0,
	)
	globalHUD.hwnd = hwnd

	// Set pill rounded region & 92% opacity
	hRgn, _, _ := procCreateRoundRectRgn.Call(0, 0, uintptr(globalHUD.w), uintptr(globalHUD.h), 26, 26)
	procSetWindowRgn.Call(hwnd, hRgn, 1)
	procSetLayeredWindowAttributes.Call(hwnd, 0, 235, LWA_ALPHA)
	globalHUD.repositionBottomCenter()

	// Start 60ms animation timer
	procSetTimer.Call(hwnd, 1, 60, 0)

	// Add System Tray Icon
	addTrayIcon(hwnd)

	// Start Global Alt & Esc Monitor
	go startKeyboardMonitor(globalMgr)

	var msg MSG
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}
