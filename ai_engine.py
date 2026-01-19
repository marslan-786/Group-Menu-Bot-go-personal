import os
import uvicorn
import subprocess
import edge_tts
import asyncio
from fastapi import FastAPI, UploadFile, File, Form
from fastapi.responses import FileResponse
from faster_whisper import WhisperModel
import torch

app = FastAPI()

# 1. SETUP PATHS
TEMP_DIR = "/app/temp_ai"
os.makedirs(TEMP_DIR, exist_ok=True)

# 2. LOAD WHISPER (Ears) - سننے کے لیے یہ ابھی بھی بیسٹ ہے
print("⏳ [PYTHON] Loading Whisper (Ears)...")
# CPU پر چلنے کے لیے int8 استعمال کر رہے ہیں تاکہ تیز ہو
stt_model = WhisperModel("large-v3", device="cuda" if torch.cuda.is_available() else "cpu", compute_type="float16" if torch.cuda.is_available() else "int8")

# 3. VOICE CONFIG (Urdu - Pakistan)
# Male: "ur-PK-SalmanNeural" | Female: "ur-PK-UzmaNeural"
# چونکہ آپ دوست/پارٹنر چاہتے ہیں، میں فی الحال 'Salman' رکھ رہا ہوں، اگر فیمیل چاہیے تو 'Uzma' کر دینا۔
VOICE_NAME = "ur-PK-SalmanNeural"

@app.post("/transcribe")
async def transcribe(file: UploadFile = File(...)):
    """User ki voice sun kar text mein badlo"""
    file_path = os.path.join(TEMP_DIR, file.filename)
    with open(file_path, "wb") as buffer:
        buffer.write(await file.read())
    
    # Transcribe
    segments, info = stt_model.transcribe(file_path, beam_size=5)
    text = "".join([segment.text for segment in segments])
    
    os.remove(file_path)
    return {"text": text, "language": info.language}

@app.post("/speak")
async def speak(text: str = Form(...), lang: str = Form("ur")):
    """
    Super Fast Cloud TTS Generation
    """
    # Random filenames to avoid collision
    rand_id = os.urandom(4).hex()
    raw_mp3_path = os.path.join(TEMP_DIR, f"raw_{rand_id}.mp3")
    final_ogg_path = os.path.join(TEMP_DIR, f"out_{rand_id}.opus")
    
    try:
        # 1. Generate Audio using Edge-TTS (Cloud) - MilliSeconds mein hoga!
        communicate = edge_tts.Communicate(text, VOICE_NAME)
        await communicate.save(raw_mp3_path)

        # 2. Convert to WhatsApp Opus (FFMPEG) - Taake 'Blue Mic' aaye aur play ho
        subprocess.run([
            "ffmpeg", "-y",
            "-i", raw_mp3_path,
            "-vn", "-c:a", "libopus", "-b:a", "16k", "-ac", "1", "-f", "ogg",
            final_ogg_path
        ], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)

    except Exception as e:
        print(f"❌ Audio Gen Error: {e}")
        return {"error": str(e)}
    
    # Cleanup MP3 (We only need Opus)
    if os.path.exists(raw_mp3_path): os.remove(raw_mp3_path)

    return FileResponse(final_ogg_path, media_type="audio/ogg")

if __name__ == "__main__":
    uvicorn.run(app, host="0.0.0.0", port=5000)
