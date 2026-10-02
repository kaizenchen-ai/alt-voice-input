# Alt Voice Input (macOS 全域極速 AI 語音聽寫潤飾守護進程)

> 🎙️ **專為 macOS 打造的極致輕量、零干擾、無字數上限的 AI 語音輸入工具。**  
> 徹底取代吃資源的 Electron 商業語音軟體（如 Typeless），重現最純粹的原生 macOS 極速體驗。

---

## 🌟 核心特色

- **零額度焦慮（白嫖 Google Gemini 免費配額）**：
  - 串接 **Google Gemini 3.5 Flash** 多模態音訊架構，平均 3 秒內完成轉錄、智慧去除口語贅字（呃、那個、就是說）、修正同音錯字並加上自然分段與標點符號。
  - 支援雙 API Key 自動輪詢與容錯備援，每日提供高達 3,000 次請求額度（每週可說 60 萬字以上）。
- **極致輕量原生架構**：
  - **背景 CPU 佔用：0.0%**（無事件時完全休眠）。
  - **記憶體佔用：約 80 MB**（相較於 Typeless 的 750MB+ 節省 90% 系統資源）。
- **macOS 原生毛玻璃懸浮膠囊（Native Cocoa HUD）**：
  - 底部浮動 Pill 膠囊，即時顯示 **分秒計時器** 與動態音波（`🔴 00:15 正在聆聽...  ▂▃`）。
  - AI 運算時即時顯示處理秒數（`✨ AI 智慧潤飾中 (1.2s) ⠋`），徹底消除「不知道有沒有在聽、有沒有在處理」的盲盒感。
- **無縫連續錄製與自動分段保護（Auto-Chunking Relay）**：
  - **零等待接力**：當前一段送出 AI 潤飾時，隨時可再按一下 Option 鍵立刻開啟新錄音，前段自動在背景排隊貼入，說話完全不被打斷。
  - **自動切段保護**：單次錄音達到 150 秒（2.5 分鐘）時，系統自動啟動零音訊間隙切段（Zero-Gap Capture），前段自動送出貼上、後段持續錄製，演講 30 分鐘也不超標、絕不遺漏任何一句心血。
- **穩健進程生命週期守護**：
  - 內建 `RLock` 與進程組管理，支援 `SIGINT ➔ SIGTERM ➔ SIGKILL` 逐級終止回收，杜絕任何 `ffmpeg` 孤兒進程殘留佔用麥克風。

---

## 🚀 快速安裝與啟用

### 1. 複製專案
```bash
git clone https://github.com/kaizenchen-ai/alt-voice-input.git
cd alt-voice-input
```

### 2. 一鍵安裝依賴與指令
```bash
chmod +x install.sh
./install.sh
```

### 3. 配置 Gemini API 金鑰
至 [Google AI Studio](https://aistudio.google.com/app/apikey) 免費申請 API Key，並填入 `~/.hermes/.env` 或本地 `.env`：
```bash
GOOGLE_API_KEY="AIzaSy..."
GOOGLE_API_KEY_FALLBACK_1="AIzaSy..."  # 可選：第二組備援 Key
```

### 4. 啟動服務
```bash
alt-voice start
```

---

## 🎮 操作方式

1. **游標就位**：在任何可輸入文字的應用程式（備忘錄、LINE、Telegram、瀏覽器、Word、IDE 等）點擊輸入框。
2. **開始錄音**：單按一下鍵盤的 **`Option`（⌥ / Alt）鍵**。
   - 聽到 **叮一聲（Tink 音效）**，螢幕底部浮現紅色懸浮膠囊與即時計時器，開始對麥克風自然說話。
3. **完成輸入**：再按一下 **`Option` 鍵**。
   - 聽到 **啵一聲（Pop 音效）**，膠囊切換為藍色 AI 智慧潤飾旋轉動態。
   - 約 **2~3 秒內** 聽到 **英雄音效（Hero 音效）**，潤飾後的繁體中文會直接精準自動貼在游標處！

> 💡 **防誤觸保護**：按著 Option 鍵搭配其他按鍵（如 `Option + C`、`Option + Tab`）時，系統自動忽略，絕不干擾正常的組合快捷鍵。

---

## 🛠️ CLI 管理指令

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
