# Alt Voice Input (macOS & Windows 雙平台全域極速 AI 語音聽寫與潤飾引擎)

> 🎙️ **專為 macOS 與 Windows 打造的極致輕量、零干擾、無字數上限的 AI 語音輸入與文字潤飾工具。**  
> 徹底取代吃資源的 Electron 商業語音軟體（如 Typeless），重現最純粹的原生系統極速體驗。

---

## 🌟 核心特色（macOS & Windows 100% 同步）

- **雙模式智慧觸發（單按一下 `Option / Alt`）**：
  - **🎤 語音口述模式（Scheme B）**：未反白文字時單按一下 `Alt`，即刻啟動麥克風錄音；再按一下 `Alt`，自動完成轉錄、清除贅字、條列排版並貼上。
  - **✨ 反白潤飾 + 自動學詞模式（Scheme A）**：滑鼠反白選取任何草稿文字後單按一下 `Alt`，AI 自動潤飾並原地替換！若系統偵測到您手動修改過前次語音辨識結果，會自動對照萃取您的**個人專屬詞彙/同音修正**並寫入記憶詞庫（`voice_vocabulary.json`），越用越聰明！
- **畫面正下方即時動態音波與 1.5 秒音量清晰度警示（Floating Pill HUD）**：
  - 懸浮膠囊固定於螢幕**正下方中央**（緊鄰輸入框視線範圍，免抬頭）。
  - 說話時即時呈現 5 段對稱動態音波（`🔴 00:03 正在聆聽...  ▂▅▇▅▂`）。
  - **即時微弱/無聲視覺預警**：若開始錄音滿 **1.5 秒**未偵測到清晰人聲（或中途聲音過小），膠囊立即亮起 **3px 亮橘色加粗警示框** 並閃爍 `⚠️ 00:01 聲音太小/聽不清楚！請靠近麥克風`，一旦開口說話即刻自動解除警示。
- **Windows 原生零依賴單一執行檔（`AltVoiceInput_Setup.exe` / `AltVoiceInput.exe`）**：
  - 純 Win32 API + WinMM 底層音訊架構編譯（檔案僅 ~6.5 MB），**免安裝 Python、免安裝 FFmpeg**，在任何 Windows 10 / 11 (64-bit) 電腦雙擊即可使用！
  - 內建數位音訊濾波（De-click 防爆音 + 220Hz 二階高通濾波消除電源電流聲 + 自動人聲增益放大）與右下角系統列（System Tray）常駐選單。

---

## 💻 Windows 版安裝與使用

### 方式一：一鍵安裝版（推薦）
1. 下載並雙擊執行 **`AltVoiceInput_Setup.exe`**。
2. 安裝程式會自動將程式部署至 `%LOCALAPPDATA%\AltVoiceInput`、建立**桌面捷徑**、設定 **Windows 開機自動啟動**，並立即在背景啟動服務（右下角系統列將出現圖示）。

### 方式二：免安裝綠色單一執行檔
1. 直接雙擊 **`AltVoiceInput.exe`** 即可常駐運行。
2. 右鍵點擊右下角系統列圖示，可隨時切換「開機自動啟動」、編輯個人專屬詞庫（`voice_vocabulary.json`）或設定 API Key（`.env`）。

### Windows 跨平台編譯（於 macOS 或 Windows 建置）
```bash
./windows/build_windows.sh
# 產出：
# - windows/dist/AltVoiceInput_Setup.exe (Windows 一鍵安裝包)
# - windows/dist/AltVoiceInput.exe       (Windows 免安裝單一執行檔)
```

---

## 🍎 macOS 版快速安裝與啟用

### 1. 複製專案與安裝
```bash
git clone https://github.com/kaizenchen-ai/alt-voice-input.git
cd alt-voice-input
chmod +x install.sh
./install.sh
```

### 2. CLI 管理指令（macOS）
```bash
alt-voice start    # 啟動背景守護進程
alt-voice stop     # 停止守護進程並徹底釋放所有音訊資源
alt-voice restart  # 重啟服務
alt-voice status   # 查看目前運行 PID、記憶體與即時狀態
alt-voice log      # 即時查看即時語音辨識與 API 調用日誌
```

---

## 📄 授權條款

MIT License. Designed with ❤️ by Teacher Kevin.
