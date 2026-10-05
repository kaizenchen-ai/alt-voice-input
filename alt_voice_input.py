#!/usr/bin/env python3
"""
Alt Voice Input - Ultra-Pure Voice Dictation Engine (Robust & Safe Edition)
Focus: Voice Input ➔ Audio Transcription ➔ AI Polishing ➔ Safe Single-Paste

Key Architectures & Safeguards:
1. Pure Dictation Workflow:
   - Single trigger via Option (Alt) tap.
   - Ultra-fast Gemini Flash multimodal polishing (removes fillers, fixes homophones, standardizes punctuation).
   - Only listens to Option (Toggle) and Esc (Cancel). NO custom hotkey combinations.
2. Anti-Recursion Paste Shield (_is_pasting Guard):
   - Completely ignores any synthetic or hardware keyboard events while pasting is in progress.
   - Prevents any runaway event-tap feedback loop.
3. P0 Sequential Order Assurance (FIFO Chunk Delivery):
   - Strict FIFO delivery for auto-chunked speech (150s segments).
   - Chunk 1 is strictly guaranteed to paste before Chunk 2.
4. Escape to Cancel (Esc 即時取消):
   - Press Esc anytime to immediately cancel recording and invalidate any pending AI workers.
5. Zero-Ghosting HUD with Generation Token Guard:
   - Native macOS Frosted Glass pill HUD.
   - Generation tokens prevent old timers from hiding or interfering with new sessions.
"""

import atexit
import os
import re
import sys
import time
import json
import base64
import signal
import shutil
import threading
import subprocess
import urllib.request
import urllib.error

try:
    import AppKit
    import Foundation
    from PyObjCTools import AppHelper
except ImportError as e:
    print(f"❌ 缺少 macOS 原生框架 (PyObjC): {e}\n請執行: pip3 install pyobjc", file=sys.stderr)
    sys.exit(1)

try:
    from pynput import keyboard
except ImportError as e:
    print(f"❌ 缺少鍵盤監聽套件 (pynput): {e}\n請執行: pip3 install pynput", file=sys.stderr)
    sys.exit(1)

# Configuration
LOG_PATH = "/tmp/alt_voice_input.log"
FFMPEG_BIN = "/opt/homebrew/bin/ffmpeg"
AFPLAY_BIN = "/usr/bin/afplay"
AUTO_CHUNK_SECONDS = 150  # 2.5 minutes auto-slice protection
DEFAULT_AUDIO_DEVICE_INDEX = 2  # Fallback used only if device enumeration fails

# Sound Effects
SOUND_START = "/System/Library/Sounds/Tink.aiff"
SOUND_STOP = "/System/Library/Sounds/Pop.aiff"
SOUND_SUCCESS = "/System/Library/Sounds/Hero.aiff"
SOUND_CANCEL = "/System/Library/Sounds/Basso.aiff"

# Spinner & Wave animation frames
SPINNER_FRAMES = ["⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"]
WAVE_FRAMES = [" ▂▃", "▂▃▄", "▃▄▅", "▄▅▆", "▅▆▇", "▆▇█", "▇█▆", "█▆▅", "▆▅▄", "▅▄▃", "▄▃▂", "▃▂ "]

# API Keys & Models
def load_env_keys():
    keys = []
    for env_var in ["GOOGLE_API_KEY_FALLBACK_1", "GOOGLE_API_KEY", "GEMINI_API_KEY"]:
        val = os.environ.get(env_var, "").strip()
        if val and val not in keys:
            keys.append(val)

    candidate_envs = [
        os.path.expanduser("~/.hermes/.env"),
        os.path.join(os.path.dirname(os.path.abspath(__file__)), ".env"),
        os.path.expanduser("~/.env")
    ]
    for env_file in candidate_envs:
        if os.path.exists(env_file):
            try:
                with open(env_file, "r", encoding="utf-8") as f:
                    for line in f:
                        line = line.strip()
                        if line.startswith("#") or "=" not in line:
                            continue
                        k, v = line.split("=", 1)
                        k, v = k.strip(), v.strip().strip('"').strip("'")
                        if k == "KEY_POOL":
                            for p in v.split(","):
                                p = p.strip()
                                if p and p not in keys:
                                    keys.append(p)
                        elif k in ("GOOGLE_API_KEY_FALLBACK_1", "GOOGLE_API_KEY", "GEMINI_API_KEY"):
                            if v and v not in keys:
                                keys.append(v)
            except Exception as e:
                log(f"Error reading env file {env_file}: {e}")
    return keys

API_KEYS = load_env_keys()
# Priority: Flash Lite has 0 rate limit and <1s latency; followed by Flash Latest and Flash 3.5
CANDIDATE_MODELS = ["gemini-flash-lite-latest", "gemini-flash-latest", "gemini-3.5-flash"]

LOCK_FILE = "/tmp/alt_voice_input.lock"
_lock_fd = None

def acquire_single_instance_lock():
    global _lock_fd
    try:
        import fcntl
        _lock_fd = open(LOCK_FILE, "w")
        fcntl.flock(_lock_fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        _lock_fd.write(str(os.getpid()))
        _lock_fd.flush()
    except (IOError, BlockingIOError):
        print("❌ Another instance of Alt Voice Input is already running. Exiting.", file=sys.stderr)
        sys.exit(0)

VOCAB_CONFIG_PATH = os.path.expanduser("~/.hermes/config/voice_vocabulary.json")

def load_vocabulary():
    default_mappings = {
        "HY": "AGY",
        "hy": "AGY",
        "A G Y": "AGY",
        "Cloud": "Claude",
        "cloudy": "Claude",
        "Cloudy": "Claude",
        "克勞德": "Claude",
        "code x": "Codex",
        "Code x": "Codex",
        "科代克斯": "Codex",
        "西西低": "CCD",
        "CCT": "CCD",
        "CCB": "CCD",
        "荷米斯": "Hermes",
        "愛馬仕": "Hermes",
        "太普利斯": "Typeless",
        "type list": "Typeless",
        "人家Drive": "仁家Drive",
        "任家Drive": "仁家Drive"
    }
    default_hints = [
        "AGY（Google Antigravity 總調度核心，發音常被誤聽為 HY、A-G-Y）",
        "Claude（Anthropic 旗艦模型，發音常被誤聽為 Cloud、cloudy、克勞德）",
        "Codex（OpenAI 幕僚長代理人，發音常被誤聽為 Code X、科代克斯）",
        "CCD（Claude Code CLI 專用簡稱，發音常被誤聽為 CCT、西西低）",
        "Hermes（本地自動化調度代理人，發音常被誤聽為 愛馬仕、荷米斯）",
        "Typeless（macOS 語音輸入工具，發音常被誤聽為 type list）",
        "仁家Drive（外接硬碟標籤 仁家Drive1T）",
        "TOEIC / 多益（英語檢定測驗）",
        "NotebookLM（Google 筆記與多模態研究工具）",
        "Alt / Option（Mac 修飾鍵）"
    ]
    if os.path.exists(VOCAB_CONFIG_PATH):
        try:
            with open(VOCAB_CONFIG_PATH, "r", encoding="utf-8") as f:
                data = json.load(f)
                mappings = data.get("mappings", default_mappings)
                hints = data.get("context_hints", default_hints)
                return mappings, hints
        except Exception as e:
            log(f"Error reading vocabulary config: {e}")
    return default_mappings, default_hints

def get_system_prompt():
    _, hints = load_vocabulary()
    hints_text = "\n".join(f"   - {h}" for h in hints)
    return f"""你是一位高階商務與專業技術的文字速記潤飾專家。
使用者是一位在 macOS 上進行 AI 代理人開發（AGY/Claude/Codex/Hermes）、程式編修與英語教學的專業工程師與教師。
使用者剛才透過語音口述了一段話。你的唯一任務是將這段口語音訊轉換為流暢、高品質的繁體中文（台灣習慣）：
1. 【嚴格清除口語贅字】：徹底刪除所有口頭禪與無意義發語詞（如：呃、額、那個、就是說、然後呢、對啊、嗯、啊等贅詞結巴）。
2. 【智慧錯字修正常理】：根據上下文自動修正同音錯字（例如將「私藥的發包」修正為「次要的報表/排程」、「AZ」修正為「Alt」等合理用詞）。
3. 【使用者專屬詞庫與發音校正（最高優先級）】：
   使用者在說話時常提及特定專有名詞，發音可能受口音影響或被語音模型聽偏。請務必結合使用者的 Mac 繁體中文工作場景，強制校正為正確專業詞彙：
{hints_text}
4. 【智慧項目條列排版】：若口述內容包含「多個並列事項、不同任務清單、執行步驟、或同類要點」，請主動排版為清晰條理的項目清單（依語意採用數字編號「1. 2. 3.」或項目符號「•」分行呈現），使其一目了然。
5. 【結構通順與標點符號】：陳述性敘述請重組為自然分段、標點正確的標準繁體中文。
6. 【輸出規範鐵則】：直接輸出修飾後的純文字內容，絕對嚴禁任何開場白、不要引號、不要任何「好的」、「這是整理後的內容」等廢話。
7. 若音訊中只有空白、純雜音或無聲音，請僅回傳 [EMPTY]。"""

def apply_vocabulary_post_processing(text):
    mappings, _ = load_vocabulary()
    for wrong, right in mappings.items():
        if re.search(r'[a-zA-Z]', wrong):
            pattern = r'\b' + re.escape(wrong) + r'\b'
            text = re.sub(pattern, right, text, flags=re.IGNORECASE)
        else:
            text = text.replace(wrong, right)
    return text

SNAPSHOT_FILE = os.path.expanduser("~/.hermes/config/voice_history_snapshots.json")

def load_history_snapshots():
    if os.path.exists(SNAPSHOT_FILE):
        try:
            with open(SNAPSHOT_FILE, "r", encoding="utf-8") as f:
                return json.load(f)
        except Exception:
            pass
    return []

def append_history_snapshot(text):
    if not text or not text.strip():
        return
    history = load_history_snapshots()
    history.append({"text": text.strip(), "timestamp": time.time()})
    if len(history) > 30:
        history = history[-30:]
    try:
        with open(SNAPSHOT_FILE, "w", encoding="utf-8") as f:
            json.dump(history, f, ensure_ascii=False, indent=2)
    except Exception as e:
        log(f"Error saving history snapshot: {e}")

def save_learned_vocabulary(new_pairs):
    if not new_pairs:
        return {}
    try:
        data = {"mappings": {}, "context_hints": []}
        if os.path.exists(VOCAB_CONFIG_PATH):
            with open(VOCAB_CONFIG_PATH, "r", encoding="utf-8") as f:
                data = json.load(f)
        
        mappings = data.setdefault("mappings", {})
        added = {}
        for wrong, right in new_pairs.items():
            wrong = str(wrong).strip().strip('"').strip("'")
            right = str(right).strip().strip('"').strip("'")
            if wrong and right and wrong != right:
                if mappings.get(wrong) != right:
                    mappings[wrong] = right
                    added[wrong] = right

        if added:
            with open(VOCAB_CONFIG_PATH, "w", encoding="utf-8") as f:
                json.dump(data, f, ensure_ascii=False, indent=2)
            log(f"🧠 [Auto-Learned Vocabulary Persisted]: {added}")
            for target_dir in ["/Users/chenkairen/Projects/alt-voice-input", "/Volumes/仁家Drive1T/06_Skills_Automation/alt_voice_input"]:
                if os.path.isdir(target_dir):
                    cfg = os.path.join(target_dir, "voice_vocabulary.json")
                    try:
                        with open(cfg, "w", encoding="utf-8") as f:
                            json.dump(data, f, ensure_ascii=False, indent=2)
                    except Exception:
                        pass
            return added
    except Exception as e:
        log(f"Error saving learned vocabulary: {e}")
    return {}

def extract_and_learn_vocabulary(orig_text, edited_text):
    if not orig_text or not edited_text or orig_text.strip() == edited_text.strip():
        return
    def worker():
        try:
            prompt = f"""請對照以下兩段文字：
【原始語音辨識輸出】：
{orig_text}

【使用者手動修正後的正確文字】：
{edited_text}

請精準萃取出使用者所做的「專用單詞、口語諧音、同音錯字」修正對照。
規則：
1. 必須將兩邊相同的周邊文字剝離，只萃取最小單位的核心詞彙（例如萃取 {{ "半百": "反白", "輪示": "潤飾", "HY": "AGY" }}）。
2. 不要包含整句或相同的前後文。
3. 嚴格輸出純 JSON 鍵值對（{{ "誤聽詞": "正確詞" }}），無 Markdown 標記，無其他說明。
若無任何特定詞彙修正，請回傳 {{}}。"""

            payload = {
                "contents": [{"parts": [{"text": prompt}]}],
                "generationConfig": {"temperature": 0.1, "topP": 0.95}
            }
            body = json.dumps(payload).encode("utf-8")
            for key in API_KEYS:
                for model in CANDIDATE_MODELS:
                    url = f"https://generativelanguage.googleapis.com/v1beta/models/{model}:generateContent?key={key}"
                    req = urllib.request.Request(url, data=body, headers={"Content-Type": "application/json"})
                    try:
                        with urllib.request.urlopen(req, timeout=4.0) as resp:
                            res = json.loads(resp.read().decode("utf-8"))
                            parts = res.get("candidates", [{}])[0].get("content", {}).get("parts", [])
                            if parts:
                                ans_text = parts[0].get("text", "").strip()
                                if ans_text.startswith("```"):
                                    ans_text = re.sub(r"^```(?:json)?\n?", "", ans_text)
                                    ans_text = re.sub(r"\n?```$", "", ans_text)
                                pairs = json.loads(ans_text)
                                if isinstance(pairs, dict) and pairs:
                                    added = save_learned_vocabulary(pairs)
                                    if added:
                                        log(f"🎉 成功自主學習新詞彙對照: {added}")
                                return
                    except Exception as e:
                        continue
        except Exception as e:
            log(f"Auto-learn worker error: {e}")

    threading.Thread(target=worker, name="auto-learn-vocab", daemon=True).start()

def log(msg):
    ts = time.strftime("%Y-%m-%d %H:%M:%S")
    formatted = f"[{ts}] {msg}"
    print(formatted, flush=True)
    try:
        with open(LOG_PATH, "a", encoding="utf-8") as f:
            f.write(formatted + "\n")
    except Exception:
        pass

def play_sound(sound_file):
    try:
        subprocess.Popen([AFPLAY_BIN, sound_file], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    except Exception as e:
        log(f"Sound play error: {e}")

def get_current_system_input_name():
    cmd = shutil.which("SwitchAudioSource") or "/opt/homebrew/bin/SwitchAudioSource"
    try:
        res = subprocess.run([cmd, "-c", "-t", "input"], capture_output=True, text=True, timeout=2)
        if res.returncode == 0:
            return res.stdout.strip()
    except Exception:
        pass
    return None

def probe_device_rms(idx):
    try:
        tmp_f = f"/tmp/probe_dev_{idx}.wav"
        subprocess.run([
            FFMPEG_BIN, "-y", "-loglevel", "error",
            "-f", "avfoundation", "-i", f":{idx}",
            "-t", "0.35", "-ar", "16000", "-ac", "1", tmp_f
        ], timeout=2.0)
        import wave, numpy as np
        if os.path.exists(tmp_f):
            with wave.open(tmp_f, "rb") as w:
                frames = w.readframes(w.getnframes())
                if frames:
                    arr = np.frombuffer(frames, dtype=np.int16).astype(np.float32)
                    rms = float(np.sqrt(np.mean(arr**2)))
                    try:
                        os.remove(tmp_f)
                    except Exception:
                        pass
                    return rms
            try:
                os.remove(tmp_f)
            except Exception:
                pass
    except Exception:
        pass
    return 0.0

def get_audio_device_index():
    try:
        proc = spawn_ffmpeg(
            [FFMPEG_BIN, "-f", "avfoundation", "-list_devices", "true", "-i", ""],
            stderr=subprocess.PIPE, stdout=subprocess.PIPE, text=True)
        try:
            _, device_output = proc.communicate(timeout=10)
        finally:
            stop_ffmpeg(proc)
        audio_section = False
        devices = []
        for line in device_output.splitlines():
            if "AVFoundation audio devices:" in line:
                audio_section = True
                continue
            if audio_section:
                m = re.search(r"\[(\d+)\]\s+(.*)", line)
                if m:
                    devices.append((int(m.group(1)), m.group(2).strip()))
        
        # Filter out virtual audio loopback drivers
        non_virtual = [d for d in devices if not any(v in d[1].lower() for v in ["blackhole", "loopback", "soundflower", "multi-output"])]

        # 1. Check system default device
        sys_def = get_current_system_input_name()
        default_dev = None
        if sys_def:
            for idx, name in non_virtual:
                if sys_def.lower() in name.lower() or name.lower() in sys_def.lower():
                    default_dev = (idx, name)
                    break
        
        # Probe default device to ensure it is not dead silence
        if default_dev:
            d_idx, d_name = default_dev
            rms = probe_device_rms(d_idx)
            if rms > 70.0 or len(non_virtual) <= 1:
                log(f"🎤 Matched active default input device: [{d_idx}] {d_name} (RMS: {rms:.1f})")
                return d_idx
            else:
                log(f"⚠️ Default device [{d_idx}] {d_name} has dead silence (RMS {rms:.1f}). Searching for active fallback...")

        # 2. Probe all non-virtual devices to find the one with active audio signal
        best_dev = None
        best_rms = -1.0
        for idx, name in non_virtual:
            rms = probe_device_rms(idx)
            log(f"🎤 Probed device [{idx}] {name} signal RMS: {rms:.1f}")
            if rms > best_rms:
                best_rms = rms
                best_dev = (idx, name)

        if best_dev and best_rms > 50.0:
            log(f"🎤 Auto-selected active audio device with signal: [{best_dev[0]}] {best_dev[1]} (RMS {best_rms:.1f})")
            try:
                subprocess.run(["SwitchAudioSource", "-s", best_dev[1], "-t", "input"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            except Exception:
                pass
            return best_dev[0]

        if default_dev:
            return default_dev[0]
        for idx, name in non_virtual:
            if "外接" in name or "External" in name:
                return idx
        for idx, name in non_virtual:
            if "麥克風" in name or "Mic" in name:
                return idx
        if non_virtual:
            return non_virtual[0][0]
        if devices:
            return devices[0][0]
    except Exception as e:
        log(f"Device detection error: {e}")
    return DEFAULT_AUDIO_DEVICE_INDEX

# All ffmpeg children stay registered until wait() has reaped them.
_ffmpeg_lock = threading.RLock()
_ffmpeg_children = set()
_shutdown = threading.Event()

def spawn_ffmpeg(cmd, **kwargs):
    with _ffmpeg_lock:
        if _shutdown.is_set():
            raise RuntimeError("Voice input is shutting down")
        proc = subprocess.Popen(cmd, start_new_session=True, **kwargs)
        _ffmpeg_children.add(proc)
        return proc

def stop_ffmpeg(proc):
    if proc is None:
        return
    with _ffmpeg_lock:
        for sig, timeout in ((signal.SIGINT, 1.5), (signal.SIGTERM, 1.0), (signal.SIGKILL, None)):
            if proc.poll() is not None:
                break
            try:
                os.killpg(proc.pid, sig)
            except ProcessLookupError:
                pass
            try:
                proc.wait(timeout=timeout)
                break
            except subprocess.TimeoutExpired:
                continue
        proc.wait()
        _ffmpeg_children.discard(proc)

def cleanup_ffmpeg():
    _shutdown.set()
    with _ffmpeg_lock:
        for proc in tuple(_ffmpeg_children):
            stop_ffmpeg(proc)

def handle_exit_signal(signum, frame):
    if _shutdown.is_set():
        return
    _shutdown.set()

    def finish():
        cleanup_ffmpeg()
        AppHelper.callAfter(AppHelper.stopEventLoop)

    threading.Thread(target=finish, name="voice-shutdown", daemon=True).start()


class FloatingHUD:
    """Native macOS Frosted Glass Pill HUD with Live Timers & Generation Guard"""
    def __init__(self):
        screen = AppKit.NSScreen.mainScreen()
        frame = screen.frame()
        self.w, self.h = 360, 52
        x = (frame.size.width - self.w) / 2
        y = 110  # Bottom-Center: right above the text input box & Dock, directly in line of sight!

        rect = Foundation.NSMakeRect(x, y, self.w, self.h)
        self.window = AppKit.NSPanel.alloc().initWithContentRect_styleMask_backing_defer_(
            rect,
            AppKit.NSWindowStyleMaskBorderless | AppKit.NSWindowStyleMaskNonactivatingPanel,
            AppKit.NSBackingStoreBuffered,
            False
        )
        self.window.setLevel_(AppKit.NSStatusWindowLevel + 50)
        self.window.setCollectionBehavior_(
            AppKit.NSWindowCollectionBehaviorCanJoinAllSpaces |
            AppKit.NSWindowCollectionBehaviorFullScreenAuxiliary
        )
        self.window.setOpaque_(False)
        self.window.setBackgroundColor_(AppKit.NSColor.clearColor())
        self.window.setIgnoresMouseEvents_(True)
        self.window.setHasShadow_(True)

        self.effect_view = AppKit.NSVisualEffectView.alloc().initWithFrame_(Foundation.NSMakeRect(0, 0, self.w, self.h))
        self.effect_view.setMaterial_(AppKit.NSVisualEffectMaterialHUDWindow)
        self.effect_view.setBlendingMode_(AppKit.NSVisualEffectBlendingModeBehindWindow)
        self.effect_view.setState_(AppKit.NSVisualEffectStateActive)
        self.effect_view.setWantsLayer_(True)
        self.effect_view.layer().setCornerRadius_(26)
        self.effect_view.layer().setMasksToBounds_(True)
        self.effect_view.layer().setBorderWidth_(1.0)
        self.effect_view.layer().setBorderColor_(AppKit.NSColor.whiteColor().colorWithAlphaComponent_(0.15).CGColor())

        self.label = AppKit.NSTextField.alloc().initWithFrame_(Foundation.NSMakeRect(10, 12, self.w - 20, 28))
        self.label.setBezeled_(False)
        self.label.setDrawsBackground_(False)
        self.label.setEditable_(False)
        self.label.setSelectable_(False)
        self.label.setAlignment_(AppKit.NSTextAlignmentCenter)
        self.label.setTextColor_(AppKit.NSColor.whiteColor())
        self.label.setFont_(AppKit.NSFont.monospacedDigitSystemFontOfSize_weight_(15, AppKit.NSFontWeightMedium))

        self.effect_view.addSubview_(self.label)
        self.window.contentView().addSubview_(self.effect_view)

        self.anim_timer = None
        self.anim_idx = 0
        self.hud_mode = "IDLE"
        self.generation = 0  # Guard token against stale fade-out timers
        self.current_rms = 0.0  # Real-time microphone audio amplitude
        
        # Timing states
        self.record_start_time = 0
        self.segment_idx = 1
        self.proc_start_time = 0

    def rms_to_wave(self, rms):
        # Maps microphone RMS amplitude into a lively 5-bar symmetric visualizer
        norm = min(1.0, max(0.0, (rms - 200) / 3800.0))
        bar_chars = [' ', ' ', '▂', '▃', '▄', '▅', '▆', '▇', '█']
        c = bar_chars[min(8, int(norm * 8))]
        m = bar_chars[min(8, int(norm * 6))]
        o = bar_chars[min(8, int(norm * 4))]
        return f"{o}{m}{c}{m}{o}"

    def show_recording(self, seg_idx=1):
        self.generation += 1
        self.hud_mode = "RECORDING"
        self.record_start_time = time.time()
        self.segment_idx = seg_idx
        self.anim_idx = 0
        self.current_rms = 0.0
        self.window.setAlphaValue_(1.0)
        self.label.setTextColor_(AppKit.NSColor.colorWithRed_green_blue_alpha_(1.0, 0.35, 0.35, 1.0))
        self._update_text()
        self.window.orderFrontRegardless()
        self._start_animation()

    def update_recording_segment(self, seg_idx):
        self.segment_idx = seg_idx

    def show_processing(self):
        self.generation += 1
        self.hud_mode = "PROCESSING"
        self.proc_start_time = time.time()
        self.anim_idx = 0
        self.current_rms = 0.0
        self.window.setAlphaValue_(1.0)
        self.label.setTextColor_(AppKit.NSColor.colorWithRed_green_blue_alpha_(0.4, 0.8, 1.0, 1.0))
        self._update_text()
        self.window.orderFrontRegardless()
        self._start_animation()

    def show_success(self, msg="✅ 潤飾完成，已自動貼上！"):
        self.generation += 1
        gen = self.generation
        self.hud_mode = "SUCCESS"
        self.current_rms = 0.0
        self._stop_animation()
        self.window.setAlphaValue_(1.0)
        self.label.setTextColor_(AppKit.NSColor.colorWithRed_green_blue_alpha_(0.3, 0.9, 0.45, 1.0))
        self.label.setStringValue_(msg)
        self.window.orderFrontRegardless()
        AppKit.NSTimer.scheduledTimerWithTimeInterval_target_selector_userInfo_repeats_(
            0.8, self, "fadeOut:", gen, False
        )

    def show_cancel(self, msg="⚠️ 錄音已取消"):
        self.generation += 1
        gen = self.generation
        self.hud_mode = "CANCEL"
        self.current_rms = 0.0
        self._stop_animation()
        self.window.setAlphaValue_(1.0)
        self.label.setTextColor_(AppKit.NSColor.colorWithRed_green_blue_alpha_(1.0, 0.7, 0.3, 1.0))
        self.label.setStringValue_(msg)
        self.window.orderFrontRegardless()
        AppKit.NSTimer.scheduledTimerWithTimeInterval_target_selector_userInfo_repeats_(
            0.8, self, "fadeOut:", gen, False
        )

    def fadeOut_(self, timer):
        timer_gen = timer.userInfo()
        if timer_gen != self.generation:
            return  # Superseded by a newer session
        AppKit.NSAnimationContext.beginGrouping()
        AppKit.NSAnimationContext.currentContext().setDuration_(0.3)
        self.window.animator().setAlphaValue_(0.0)
        AppKit.NSAnimationContext.endGrouping()
        AppKit.NSTimer.scheduledTimerWithTimeInterval_target_selector_userInfo_repeats_(
            0.35, self, "finishHide:", timer_gen, False
        )

    def finishHide_(self, timer):
        timer_gen = timer.userInfo()
        if timer_gen != self.generation:
            return
        if self.hud_mode == "RECORDING":
            return
        self.window.orderOut_(None)
        self.hud_mode = "IDLE"

    def _start_animation(self):
        self._stop_animation()
        self.anim_timer = AppKit.NSTimer.scheduledTimerWithTimeInterval_target_selector_userInfo_repeats_(
            0.06, self, "timerTick:", None, True
        )

    def _stop_animation(self):
        if self.anim_timer:
            self.anim_timer.invalidate()
            self.anim_timer = None

    def timerTick_(self, timer):
        self.anim_idx += 1
        self._update_text()

    def _update_text(self):
        if self.hud_mode == "RECORDING":
            elapsed = time.time() - self.record_start_time
            mins, secs = divmod(int(elapsed), 60)
            wave = WAVE_FRAMES[self.anim_idx % len(WAVE_FRAMES)]
            if self.segment_idx > 1:
                self.label.setStringValue_(f"🔴 {mins:02d}:{secs:02d} 正在聆聽 [第{self.segment_idx}段] {wave}")
            else:
                self.label.setStringValue_(f"🔴 {mins:02d}:{secs:02d} 正在聆聽... {wave}")
        elif self.hud_mode == "PROCESSING":
            proc_dt = time.time() - self.proc_start_time
            spin = SPINNER_FRAMES[self.anim_idx % len(SPINNER_FRAMES)]
            self.label.setStringValue_(f"✨ AI 智慧潤飾中 ({proc_dt:.1f}s) {spin}")


class AltVoiceInputManager:
    def __init__(self, hud):
        self.hud = hud
        self.device_idx = get_audio_device_index()
        log(f"Initialized AltVoiceInputManager with audio device index: {self.device_idx}")
        
        self.is_recording = False
        self.ffmpeg_proc = None
        self.current_recording_path = None
        self.record_total_start = 0
        self.seg_start_time = 0
        self.segment_idx = 1
        self.lock = threading.Lock()
        
        # Session & Sequential FIFO Delivery Guard
        self.session_token = 0
        self.next_deliver_seq = 1
        self.pending_results = {}  # seq -> text
        self.delivery_lock = threading.Lock()
        self.active_tasks = 0

        # Anti-Recursion Shield (Blocks keyboard events during programmatic paste)
        self._is_pasting = False
        self.paste_lock = threading.Lock()

        # Key state tracking (Option and Esc ONLY)
        self.alt_pressed = False
        self.alt_press_time = 0
        self.other_key_pressed = False
        self.last_toggle_time = 0

        # Background watchdog for auto-chunking
        threading.Thread(target=self._auto_chunk_watchdog, daemon=True).start()

    def _auto_chunk_watchdog(self):
        """Monitors active recording and auto-slices when reaching AUTO_CHUNK_SECONDS"""
        while not _shutdown.wait(0.5):
            if self.is_recording:
                seg_elapsed = time.time() - self.seg_start_time
                if seg_elapsed >= AUTO_CHUNK_SECONDS:
                    self._auto_chunk_rotate(seg_elapsed)

    def _auto_chunk_rotate(self, seg_elapsed):
        with self.lock:
            if _shutdown.is_set() or not self.is_recording:
                return
            old_file = self.current_recording_path
            old_proc = self.ffmpeg_proc
            old_seq = self.segment_idx
            old_capture_th = getattr(self, 'capture_thread', None)
            current_token = self.session_token
            
            new_file = f"/tmp/alt_voice_input_{int(time.time() * 1000)}.wav"
            cmd = [
                FFMPEG_BIN,
                "-y",
                "-loglevel", "error",
                "-f", "avfoundation",
                "-i", f":{self.device_idx}",
                "-ar", "16000",
                "-ac", "1",
                new_file
            ]
            try:
                self.ffmpeg_proc = spawn_ffmpeg(cmd, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            except Exception as e:
                log(f"Failed to spawn rotated ffmpeg: {e}")
                return

            self.segment_idx += 1
            self.seg_start_time = time.time()
            self.current_recording_path = new_file
            log(f"🔄 [Auto Chunk] Reached {seg_elapsed:.1f}s. Rotating to segment {self.segment_idx}...")
            stop_ffmpeg(old_proc)

            # Update HUD to reflect new segment
            AppHelper.callAfter(self.hud.update_recording_segment, self.segment_idx)

            # Dispatch old audio chunk to AI worker with session & sequence token
            self.active_tasks += 1
            threading.Thread(
                target=self._process_worker,
                args=(old_file, seg_elapsed, True, current_token, old_seq),
                daemon=True
            ).start()

    def on_press(self, key):
        # Anti-recursion shield: Ignore any events while pasting!
        if self._is_pasting:
            return

        # Esc Key: Instant Cancel
        if key == keyboard.Key.esc:
            if self.is_recording or self.active_tasks > 0:
                log("🛑 Esc pressed. Cancelling active voice session...")
                self.cancel_session()
                return

        # Option Key tracking
        if key in (keyboard.Key.alt, keyboard.Key.alt_l, keyboard.Key.alt_r):
            if not self.alt_pressed:
                self.alt_pressed = True
                self.alt_press_time = time.time()
                self.other_key_pressed = False
        else:
            if self.alt_pressed:
                self.other_key_pressed = True

    def on_release(self, key):
        # Anti-recursion shield: Ignore any events while pasting!
        if self._is_pasting:
            return

        if key in (keyboard.Key.alt, keyboard.Key.alt_l, keyboard.Key.alt_r):
            if self.alt_pressed:
                press_duration = time.time() - self.alt_press_time
                was_solo = not self.other_key_pressed
                self.alt_pressed = False
                
                # Tap detection: single Option tap without other keys held
                if was_solo and 0.02 <= press_duration <= 1.2:
                    now = time.time()
                    if now - self.last_toggle_time < 0.35:
                        log("⚠️ Toggle debounced (too fast).")
                        return
                    self.last_toggle_time = now
                    log(f"🔘 Option tapped ({press_duration:.2f}s). Toggling recording...")
                    self.toggle()
                elif not was_solo:
                    log("ℹ️ Option combination key released, skipping dictation toggle.")
                else:
                    log(f"ℹ️ Option key duration ({press_duration:.2f}s) outside tap threshold.")

    def cancel_session(self):
        """Cancels recording or invalidates in-flight AI processing immediately."""
        with self.lock:
            # Advance token to invalidate all in-flight workers of this session
            self.session_token += 1
            was_rec = self.is_recording
            proc = self.ffmpeg_proc
            audio_file = self.current_recording_path
            self.is_recording = False
            self.ffmpeg_proc = None
            self.current_recording_path = None

        with self.delivery_lock:
            self.pending_results.clear()
            self.next_deliver_seq = 1

        if was_rec and proc is not None:
            stop_ffmpeg(proc)

        if audio_file and os.path.exists(audio_file):
            try:
                os.remove(audio_file)
            except Exception:
                pass

        play_sound(SOUND_CANCEL)
        AppHelper.callAfter(self.hud.show_cancel, "🛑 語音輸入已取消")
        log("Session cancelled cleanly by user.")

    def toggle(self):
        with self.lock:
            if _shutdown.is_set():
                return
            if self.is_recording:
                self.stop_recording_and_process()
                return

        # Scheme A: Polish selected text with Gemini Flash-Lite
        threading.Thread(target=self._polish_selection_worker, daemon=True).start()

    def _polish_selection_worker(self):
        with self.paste_lock:
            self._is_pasting = True
            try:
                # 1. Read current clipboard
                p_old = subprocess.Popen(["pbpaste"], stdout=subprocess.PIPE)
                old_clip, _ = p_old.communicate()
                old_clip = old_clip.decode("utf-8", errors="ignore")

                # 2. Put unique sentinel
                sentinel = f"__SENTINEL_{time.time()}__"
                p_set = subprocess.Popen(["pbcopy"], stdin=subprocess.PIPE)
                p_set.communicate(input=sentinel.encode("utf-8"))
                time.sleep(0.03)

                # 3. Simulate Cmd+C to copy selected text
                subprocess.run(
                    ["osascript", "-e", 'tell application "System Events" to keystroke "c" using command down'],
                    stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL
                )
                time.sleep(0.10)

                # 4. Read copied text
                p_new = subprocess.Popen(["pbpaste"], stdout=subprocess.PIPE)
                new_clip, _ = p_new.communicate()
                new_clip = new_clip.decode("utf-8", errors="ignore")
            finally:
                self._is_pasting = False

        if new_clip == sentinel or not new_clip.strip():
            # No text selected -> Seamlessly enter Scheme B (Voice Dictation with Live Audio Wave)
            with self.paste_lock:
                self._is_pasting = True
                try:
                    p_res = subprocess.Popen(["pbcopy"], stdin=subprocess.PIPE)
                    p_res.communicate(input=old_clip.encode("utf-8"))
                finally:
                    self._is_pasting = False

            log("🎤 [Scheme B] No text highlighted. Starting voice recording mode...")
            with self.lock:
                if not self.is_recording and not _shutdown.is_set():
                    self.start_recording()
            return

        # Selected text captured!
        log(f"📝 [Scheme A] Selected text ({len(new_clip)} chars): {new_clip[:40]}...")
        AppHelper.callAfter(self.hud.show_processing)

        # 🧠 Auto-Learn: Check if user modified a previous recognition
        try:
            history = load_history_snapshots()
            if history:
                now = time.time()
                for item in reversed(history):
                    if now - item.get("timestamp", 0) < 900:  # Within 15 minutes
                        orig = item.get("text", "")
                        if orig and orig.strip() != new_clip.strip():
                            s1 = set(orig)
                            s2 = set(new_clip)
                            overlap = len(s1 & s2) / max(1, len(s1 | s2))
                            if overlap > 0.30:
                                log(f"🔍 [Auto-Learn] Detected user modification compared to previous recognition! Overlap: {overlap:.2f}")
                                extract_and_learn_vocabulary(orig, new_clip)
                                break
        except Exception as e:
            log(f"Auto-learn inspection error: {e}")

        polished, err = self._call_gemini_text(new_clip)
        if not polished or err:
            log(f"❌ Text polishing failed: {err}")
            play_sound(SOUND_CANCEL)
            AppHelper.callAfter(self.hud.show_cancel, "❌ 潤飾失敗，請重試")
            with self.paste_lock:
                self._is_pasting = True
                try:
                    p_res = subprocess.Popen(["pbcopy"], stdin=subprocess.PIPE)
                    p_res.communicate(input=new_clip.encode("utf-8"))
                finally:
                    self._is_pasting = False
            return

        final_text = apply_vocabulary_post_processing(polished)
        log(f"✨ [Scheme A Result]:\n{final_text}")

        # Paste back to replace selection
        self._paste_text(final_text)
        play_sound(SOUND_SUCCESS)
        AppHelper.callAfter(self.hud.show_success, "✅ 潤飾完成，已自動替換！")

    def start_recording(self):
        self.session_token += 1
        self.is_recording = True
        self.record_total_start = time.time()
        self.seg_start_time = time.time()
        self.segment_idx = 1
        self.device_idx = get_audio_device_index()

        with self.delivery_lock:
            self.pending_results.clear()
            self.next_deliver_seq = 1
        
        audio_file = f"/tmp/alt_voice_input_{int(time.time() * 1000)}.wav"
        self.current_recording_path = audio_file
        
        log(f"🎤 [Start Recording] Session: {self.session_token}, File: {audio_file}")
        play_sound(SOUND_START)
        AppHelper.callAfter(self.hud.show_recording, 1)

        cmd = [
            FFMPEG_BIN,
            "-y",
            "-loglevel", "error",
            "-f", "avfoundation",
            "-i", f":{self.device_idx}",
            "-ar", "16000",
            "-ac", "1",
            audio_file
        ]
        try:
            self.ffmpeg_proc = spawn_ffmpeg(cmd, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        except Exception as e:
            log(f"Failed to start ffmpeg: {e}")
            self.is_recording = False
            AppHelper.callAfter(self.hud.show_cancel, "❌ 麥克風啟動失敗")

    def stop_recording_and_process(self):
        self.is_recording = False
        seg_elapsed = time.time() - self.seg_start_time
        total_elapsed = time.time() - self.record_total_start
        proc = self.ffmpeg_proc
        audio_file = self.current_recording_path
        current_token = self.session_token
        current_seq = self.segment_idx
        self.ffmpeg_proc = None

        log(f"⏹️ [Stop Recording] Segment {current_seq}: {seg_elapsed:.2f}s, Total: {total_elapsed:.2f}s. Processing...")
        play_sound(SOUND_STOP)

        stop_ffmpeg(proc)

        self.active_tasks += 1
        AppHelper.callAfter(self.hud.show_processing)

        threading.Thread(
            target=self._process_worker,
            args=(audio_file, seg_elapsed, False, current_token, current_seq),
            daemon=True
        ).start()

    def _process_worker(self, audio_file, elapsed, is_auto_chunk, token, seq):
        try:
            time.sleep(0.15)
            if token != self.session_token:
                log(f"Worker for session {token} discarded (stale).")
                return

            if not os.path.exists(audio_file):
                log(f"Audio file missing for seq {seq}.")
                self._advance_empty_seq(token, seq)
                return

            file_size = os.path.getsize(audio_file)
            if file_size < 3000 or elapsed < 0.3:
                log(f"Recording too short ({elapsed:.2f}s, {file_size} bytes). Discarded.")
                self._advance_empty_seq(token, seq)
                if not self.is_recording and not is_auto_chunk and self.active_tasks <= 1:
                    play_sound(SOUND_CANCEL)
                    AppHelper.callAfter(self.hud.show_cancel, "⚠️ 說話時間過短")
                return

            with open(audio_file, "rb") as f:
                audio_b64 = base64.b64encode(f.read()).decode("utf-8")

            text, api_error = self._call_gemini_multimodal(audio_b64)
            if token != self.session_token:
                log(f"Session {token} cancelled during API call, dropping seq {seq}.")
                return

            if text and text != "[EMPTY]" and text.strip():
                # Pipeline through text refiner for perfect bulleting & filler removal!
                raw_stt = text.strip()
                try:
                    polished_text, p_err = self._call_gemini_text(raw_stt)
                    if polished_text and not p_err:
                        raw_stt = polished_text
                except Exception as e:
                    log(f"Secondary text polish fallback: {e}")
                final_text = apply_vocabulary_post_processing(raw_stt)
                log(f"✨ [AI Result Seq {seq}]:\n{final_text}")
                self._enqueue_delivery(token, seq, final_text)
            elif api_error:
                log(f"❌ [AI Error Seq {seq}]: {api_error}")
                self._advance_empty_seq(token, seq)
                if not self.is_recording and not is_auto_chunk and self.active_tasks <= 1:
                    play_sound(SOUND_CANCEL)
                    AppHelper.callAfter(self.hud.show_cancel, "❌ AI 服務連線失敗")
            else:
                log(f"⚠️ [AI Result Seq {seq}]: No speech recognized.")
                self._advance_empty_seq(token, seq)
                if not self.is_recording and not is_auto_chunk and self.active_tasks <= 1:
                    play_sound(SOUND_CANCEL)
                    AppHelper.callAfter(self.hud.show_cancel, "⚠️ 未辨識出有效聲音")
        except Exception as e:
            log(f"Process worker error in seq {seq}: {e}")
            self._advance_empty_seq(token, seq)
            if not self.is_recording and not is_auto_chunk:
                play_sound(SOUND_CANCEL)
                AppHelper.callAfter(self.hud.show_cancel, "❌ 辨識處理異常")
        finally:
            with self.lock:
                self.active_tasks = max(0, self.active_tasks - 1)
            try:
                if os.path.exists(audio_file):
                    os.remove(audio_file)
            except Exception:
                pass

    def _enqueue_delivery(self, token, seq, text):
        """P0 Sequential Order Queue: Guarantees chunk 1 pastes before chunk 2."""
        with self.delivery_lock:
            if token != self.session_token:
                return
            self.pending_results[seq] = text
            self._drain_delivery(token)

    def _advance_empty_seq(self, token, seq):
        """Advances delivery pointer when a segment yields no text to avoid blocking later chunks."""
        with self.delivery_lock:
            if token != self.session_token:
                return
            if self.next_deliver_seq == seq:
                self.next_deliver_seq += 1
                self._drain_delivery(token)

    def _drain_delivery(self, token):
        while self.next_deliver_seq in self.pending_results:
            text = self.pending_results.pop(self.next_deliver_seq)
            log(f"📦 [Sequential Deliver Seq {self.next_deliver_seq}]: {text}")
            self._paste_text(text)
            self.next_deliver_seq += 1

            if not self.is_recording and not self.pending_results:
                play_sound(SOUND_SUCCESS)
                AppHelper.callAfter(self.hud.show_success)

    def _call_gemini_multimodal(self, audio_b64):
        payload = {
            "contents": [{
                "parts": [
                    {"text": get_system_prompt()},
                    {"inline_data": {"mime_type": "audio/wav", "data": audio_b64}}
                ]
            }],
            "generationConfig": {
                "temperature": 0.2,
                "topP": 0.95
            }
        }
        body = json.dumps(payload).encode("utf-8")

        last_error = None
        for key in API_KEYS:
            for model in CANDIDATE_MODELS:
                url = f"https://generativelanguage.googleapis.com/v1beta/models/{model}:generateContent?key={key}"
                timeout = 7.0
                req = urllib.request.Request(url, data=body, headers={"Content-Type": "application/json"})
                try:
                    t0 = time.time()
                    with urllib.request.urlopen(req, timeout=timeout) as resp:
                        res = json.loads(resp.read().decode("utf-8"))
                        dt = time.time() - t0
                        candidate = res.get("candidates", [{}])[0]
                        parts = candidate.get("content", {}).get("parts", [])
                        if parts:
                            ans = parts[0].get("text", "").strip()
                            log(f"Gemini API ({model}) returned in {dt:.2f}s")
                            return ans, None
                        log(f"Gemini API ({model}) returned no parts in {dt:.2f}s")
                        return "", None
                except urllib.error.HTTPError as e:
                    last_error = f"HTTP {e.code}: {e.reason}"
                    log(f"Gemini API {model} HTTP {e.code}: {e.reason}")
                    continue
                except (urllib.error.URLError, TimeoutError, json.JSONDecodeError) as e:
                    last_error = str(e)
                    log(f"Gemini API {model} network/parse error: {e}")
                    continue
                except Exception as e:
                    last_error = str(e)
                    log(f"Gemini API {model} failed: {e}")
                    continue
        return None, (last_error or "未知錯誤")

    def _call_gemini_text(self, raw_text):
        sys_prompt = get_system_prompt()
        prompt = (
            f"{sys_prompt}\n\n"
            f"使用者透過鍵盤反白選取的待潤飾草稿如下：\n"
            f"```text\n{raw_text}\n```\n"
            "請直接輸出潤飾排版後的繁體中文結果："
        )
        payload = {
            "contents": [{
                "parts": [{"text": prompt}]
            }],
            "generationConfig": {
                "temperature": 0.2,
                "topP": 0.95
            }
        }
        body = json.dumps(payload).encode("utf-8")

        last_error = None
        for key in API_KEYS:
            for model in CANDIDATE_MODELS:
                url = f"https://generativelanguage.googleapis.com/v1beta/models/{model}:generateContent?key={key}"
                timeout = 5.0
                req = urllib.request.Request(url, data=body, headers={"Content-Type": "application/json"})
                try:
                    t0 = time.time()
                    with urllib.request.urlopen(req, timeout=timeout) as resp:
                        res = json.loads(resp.read().decode("utf-8"))
                        dt = time.time() - t0
                        candidate = res.get("candidates", [{}])[0]
                        parts = candidate.get("content", {}).get("parts", [])
                        if parts:
                            ans = parts[0].get("text", "").strip()
                            log(f"Gemini Text API ({model}) returned in {dt:.2f}s")
                            return ans, None
                        return "", None
                except Exception as e:
                    last_error = str(e)
                    continue
        return None, (last_error or "連線失敗")

    def _paste_text(self, text):
        """Copies text to clipboard and issues a single Command+V with strict anti-recursion shielding."""
        with self.paste_lock:
            try:
                self._is_pasting = True
                p = subprocess.Popen(["pbcopy"], stdin=subprocess.PIPE)
                p.communicate(input=text.encode("utf-8"))
                time.sleep(0.04)
                ascript = 'tell application "System Events" to keystroke "v" using command down'
                subprocess.run(["osascript", "-e", ascript], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                time.sleep(0.08)  # Debounce synthetic key release
                append_history_snapshot(text)
            finally:
                self._is_pasting = False


def main():
    acquire_single_instance_lock()
    atexit.register(cleanup_ffmpeg)
    signal.signal(signal.SIGTERM, handle_exit_signal)
    signal.signal(signal.SIGINT, handle_exit_signal)
    listener = None
    try:
        log("=== Starting Alt Voice Input (Safe Pure Dictation Engine) ===")
        if not os.path.exists(FFMPEG_BIN):
            log(f"❌ 致命錯誤: 找不到 ffmpeg ({FFMPEG_BIN})，請先安裝: brew install ffmpeg")
            sys.exit(1)
        app = AppKit.NSApplication.sharedApplication()
        app.setActivationPolicy_(AppKit.NSApplicationActivationPolicyAccessory)
        hud = FloatingHUD()
        manager = AltVoiceInputManager(hud)
        if _shutdown.is_set():
            return
        listener = keyboard.Listener(on_press=manager.on_press, on_release=manager.on_release)
        listener.start()
        log("Listening for Option (Dictate) and Esc (Cancel). Running loop...")

        def signal_heartbeat():
            if not _shutdown.is_set():
                AppHelper.callLater(0.2, signal_heartbeat)

        if not _shutdown.is_set():
            AppHelper.callLater(0.2, signal_heartbeat)
            app.run()
    finally:
        cleanup_ffmpeg()
        if listener is not None:
            listener.stop()


if __name__ == "__main__":
    main()
