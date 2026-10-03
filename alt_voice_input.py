#!/usr/bin/env python3
"""
Alt Voice Input - Ultra-Pure Voice Dictation Engine
Focus: Voice Input ➔ Audio Transcription ➔ AI Polishing ➔ Auto-Paste

Key Features:
1. Pure Dictation Workflow:
   - Single trigger via Option (Alt) tap.
   - Ultra-fast Gemini 3.5 Flash / Flash Latest multimodal polishing.
   - Pristine Taiwan Traditional Chinese (removes fillers, fixes homophones, standardizes punctuation).
2. P0 Sequential Order Assurance (保序隊列):
   - Strict FIFO chunk delivery for auto-chunked long speech (150s slicing).
   - Zero chance of Chunk 2 pasting before Chunk 1, even under network jitter.
3. Escape to Cancel (Esc 秒級取消):
   - Press Esc anytime to instantly cancel recording or drop in-flight AI processing.
   - Zero residue, no accidental pasting, no wasted API resources.
4. Ctrl + Cmd + V Instant Re-Paste (防呆重貼):
   - Automatically caches the latest polished text.
   - Hit Ctrl + Cmd + V to re-paste the latest transcript without speaking again.
5. Zero-Ghosting HUD with Generation Guard:
   - Native macOS Frosted Glass pill HUD with real-time timer and animated wave.
   - Generation tokens prevent stale fade-out timers from hiding active recordings.
"""

import atexit
import os
import re
import sys
import time
import json
import base64
import signal
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
                        if k in ("GOOGLE_API_KEY_FALLBACK_1", "GOOGLE_API_KEY", "GEMINI_API_KEY"):
                            if v and v not in keys:
                                keys.append(v)
            except Exception as e:
                log(f"Error reading env file {env_file}: {e}")
    return keys

API_KEYS = load_env_keys()
CANDIDATE_MODELS = ["gemini-3.5-flash", "gemini-3-flash-preview", "gemini-flash-latest"]

SYSTEM_PROMPT = """你是一位高階商務與專業技術的文字速記潤飾專家。
使用者剛才透過語音口述了一段話。你的任務是將這段口語音訊轉換為流暢、高品質的繁體中文（台灣習慣）：
1. 【嚴格清除所有口語贅字】：徹底刪除所有口頭禪與無意義發語詞（如：呃、額、那個、就是說、然後呢、對啊、嗯、啊等結巴贅詞）。
2. 【智慧錯字修正常理】：請根據上下文修正語音同音錯字（例如將「私藥的發包」修正為「次要的報表/次要的排程」、「AZ」修正為「Alt」等合理用詞）。
3. 【結構通順與標點符號】：口語說話往往破碎或缺乏標點，請重組成文法通順、自然分段、標點正確的標準繁體中文。
4. 【輸出規範鐵則】：直接輸出修飾後的純文字內容，絕對嚴禁任何開場白、不要引號、不要任何「好的」、「這是整理後的內容」等廢話。
5. 若音訊中只有空白、純雜音或無聲音，請僅回傳 [EMPTY]。"""

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
        
        for idx, name in devices:
            if "外接" in name or "External" in name:
                return idx
        for idx, name in devices:
            if "麥克風" in name or "Mic" in name:
                return idx
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
        self.w, self.h = 340, 52
        x = (frame.size.width - self.w) / 2
        y = 75

        rect = Foundation.NSMakeRect(x, y, self.w, self.h)
        self.window = AppKit.NSPanel.alloc().initWithContentRect_styleMask_backing_defer_(
            rect,
            AppKit.NSWindowStyleMaskBorderless | AppKit.NSWindowStyleMaskNonactivatingPanel,
            AppKit.NSBackingStoreBuffered,
            False
        )
        self.window.setLevel_(AppKit.NSStatusWindowLevel + 20)
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
        
        # Timing states
        self.record_start_time = 0
        self.segment_idx = 1
        self.proc_start_time = 0

    def show_recording(self, seg_idx=1):
        self.generation += 1
        self.hud_mode = "RECORDING"
        self.record_start_time = time.time()
        self.segment_idx = seg_idx
        self.anim_idx = 0
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
        self.window.setAlphaValue_(1.0)
        self.label.setTextColor_(AppKit.NSColor.colorWithRed_green_blue_alpha_(0.4, 0.8, 1.0, 1.0))
        self._update_text()
        self.window.orderFrontRegardless()
        self._start_animation()

    def show_success(self, msg="✅ 潤飾完成，已自動貼上！"):
        self.generation += 1
        gen = self.generation
        self.hud_mode = "SUCCESS"
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
        self._stop_animation()
        self.window.setAlphaValue_(1.0)
        self.label.setTextColor_(AppKit.NSColor.colorWithRed_green_blue_alpha_(1.0, 0.7, 0.3, 1.0))
        self.label.setStringValue_(msg)
        self.window.orderFrontRegardless()
        AppKit.NSTimer.scheduledTimerWithTimeInterval_target_selector_userInfo_repeats_(
            0.8, self, "fadeOut:", gen, False
        )

    def show_toast(self, msg):
        self.generation += 1
        gen = self.generation
        self.hud_mode = "TOAST"
        self._stop_animation()
        self.window.setAlphaValue_(1.0)
        self.label.setTextColor_(AppKit.NSColor.colorWithRed_green_blue_alpha_(0.35, 0.75, 1.0, 1.0))
        self.label.setStringValue_(msg)
        self.window.orderFrontRegardless()
        AppKit.NSTimer.scheduledTimerWithTimeInterval_target_selector_userInfo_repeats_(
            1.0, self, "fadeOut:", gen, False
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
            0.1, self, "timerTick:", None, True
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
                self.label.setStringValue_(f"🔴 {mins:02d}:{secs:02d} 聆聽中 [第{self.segment_idx}段] {wave}")
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

        # Caching for instant re-paste (Ctrl + Cmd + V)
        self.last_transcript = ""

        # Key state tracking
        self.alt_pressed = False
        self.alt_press_time = 0
        self.other_key_pressed = False
        self.ctrl_pressed = False
        self.cmd_pressed = False

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
            current_token = self.session_token
            
            # Launch the next segment before stopping the previous one
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
        # 1. Track Modifiers
        if key in (keyboard.Key.ctrl, keyboard.Key.ctrl_l, keyboard.Key.ctrl_r):
            self.ctrl_pressed = True
        elif key in (keyboard.Key.cmd, keyboard.Key.cmd_l, keyboard.Key.cmd_r):
            self.cmd_pressed = True
        elif key in (keyboard.Key.alt, keyboard.Key.alt_l, keyboard.Key.alt_r):
            if not self.alt_pressed:
                self.alt_pressed = True
                self.alt_press_time = time.time()
                self.other_key_pressed = False
        else:
            if self.alt_pressed:
                self.other_key_pressed = True

        # 2. ESC Cancel (Immediate cancel during recording or processing)
        if key == keyboard.Key.esc:
            if self.is_recording or self.active_tasks > 0:
                log("🛑 Esc pressed. Cancelling active voice session...")
                self.cancel_session()
                return

        # 3. Ctrl + Cmd + V Instant Re-Paste
        is_v = False
        if hasattr(key, 'char') and key.char in ('v', 'V', '\x16'):
            is_v = True
        elif getattr(key, 'vk', None) == 9:
            is_v = True

        if is_v and self.ctrl_pressed and self.cmd_pressed:
            log("📋 Ctrl + Cmd + V detected. Triggering instant re-paste...")
            self.re_paste_last()
            return

    def on_release(self, key):
        if key in (keyboard.Key.ctrl, keyboard.Key.ctrl_l, keyboard.Key.ctrl_r):
            self.ctrl_pressed = False
        elif key in (keyboard.Key.cmd, keyboard.Key.cmd_l, keyboard.Key.cmd_r):
            self.cmd_pressed = False
        elif key in (keyboard.Key.alt, keyboard.Key.alt_l, keyboard.Key.alt_r):
            if self.alt_pressed:
                press_duration = time.time() - self.alt_press_time
                was_solo = not self.other_key_pressed
                self.alt_pressed = False
                
                # Tap detection: single Option tap without other keys held
                if was_solo and 0.02 <= press_duration <= 1.2:
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

    def re_paste_last(self):
        """Instantly pastes the most recently polished text without making any network call."""
        if not self.last_transcript:
            log("No cached transcript available for re-paste.")
            AppHelper.callAfter(self.hud.show_cancel, "⚠️ 尚無可重貼的文字紀錄")
            play_sound(SOUND_CANCEL)
            return

        log(f"Re-pasting cached text ({len(self.last_transcript)} chars)...")
        self._paste_text(self.last_transcript)
        play_sound(SOUND_SUCCESS)
        AppHelper.callAfter(self.hud.show_toast, "📋 已重新貼上！")

    def toggle(self):
        with self.lock:
            if _shutdown.is_set():
                return
            if not self.is_recording:
                self.start_recording()
            else:
                self.stop_recording_and_process()

    def start_recording(self):
        self.session_token += 1
        self.is_recording = True
        self.record_total_start = time.time()
        self.seg_start_time = time.time()
        self.segment_idx = 1

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
                log(f"✨ [AI Result Seq {seq}]: {text}")
                self._enqueue_delivery(token, seq, text.strip())
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
            self.last_transcript = text
            self.next_deliver_seq += 1

            if not self.is_recording and not self.pending_results:
                play_sound(SOUND_SUCCESS)
                AppHelper.callAfter(self.hud.show_success)

    def _call_gemini_multimodal(self, audio_b64):
        payload = {
            "contents": [{
                "parts": [
                    {"text": SYSTEM_PROMPT},
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

    def _paste_text(self, text):
        p = subprocess.Popen(["pbcopy"], stdin=subprocess.PIPE)
        p.communicate(input=text.encode("utf-8"))
        time.sleep(0.05)
        ascript = 'tell application "System Events" to keystroke "v" using command down'
        subprocess.run(["osascript", "-e", ascript], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def main():
    atexit.register(cleanup_ffmpeg)
    signal.signal(signal.SIGTERM, handle_exit_signal)
    signal.signal(signal.SIGINT, handle_exit_signal)
    listener = None
    try:
        log("=== Starting Alt Voice Input (Ultra-Pure Dictation Engine) ===")
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
        log("Listening for Option (Dictate), Esc (Cancel), and Ctrl+Cmd+V (Re-paste). Running loop...")

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
