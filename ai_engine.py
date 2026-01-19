import os
import uvicorn
import subprocess
from fastapi import FastAPI, UploadFile, File, Form
from fastapi.responses import FileResponse
from faster_whisper import WhisperModel
import torch

app = FastAPI()

# Setup Paths
TEMP_DIR = "/app/temp_ai"
MODEL_PATH = "/app/models/ur_pk.onnx" # Dockerfile se aya hai
PIPER_BIN = "/usr/local/bin/piper/piper" # Binary path

os.makedirs(TEMP_DIR, exist_ok=True)

# Load Whisper (Using your heavy CPU power)
print("⏳ [PYTHON] Loading Whisper (Ears)...")
stt_model = WhisperModel("large-v3", device="cpu", compute_type="int8")

@app.post("/transcribe")
async def transcribe(file: UploadFile = File(...)):
    file_path = os.path.join(TEMP_DIR, file.filename)
    with open(file_path, "wb") as buffer:
        buffer.write(await file.read())
    
    # CPU par 32 cores hain, beam_size barha do quality ke liye
    segments, info = stt_model.transcribe(file_path, beam_size=8)
    text = "".join([segment.text for segment in segments])
    
    os.remove(file_path)
    return {"text": text, "language": info.language}

@app.post("/speak")
async def speak(text: str = Form(...), lang: str = Form("ur")):
    rand_id = os.urandom(4).hex()
    raw_wav_path = os.path.join(TEMP_DIR, f"raw_{rand_id}.wav")
    final_ogg_path = os.path.join(TEMP_DIR, f"out_{rand_id}.opus")
    
    try:
        # 🔥 STEP 1: Generate Audio using Local Piper
        # Ye aapke 32 vCPUs ko use karega aur milliseconds mein audio banaye ga
        cmd_piper = f'echo "{text}" | {PIPER_BIN} --model {MODEL_PATH} --output_file {raw_wav_path}'
        
        # Shell=True isliye kyunke hum echo pipe kar rahe hain
        subprocess.run(cmd_piper, shell=True, check=True)

        # Check file
        if not os.path.exists(raw_wav_path) or os.path.getsize(raw_wav_path) == 0:
            return {"error": "Piper generated empty file"}

        # 🔥 STEP 2: Convert to WhatsApp OGG
        cmd_ffmpeg = [
            "ffmpeg", "-y",
            "-i", raw_wav_path,
            "-vn", "-c:a", "libopus", "-b:a", "24k", "-ac", "1", "-f", "ogg", 
            final_ogg_path
        ]
        subprocess.run(cmd_ffmpeg, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)

    except Exception as e:
        print(f"❌ Piper Error: {e}")
        return {"error": str(e)}
    
    if os.path.exists(raw_wav_path): os.remove(raw_wav_path)

    return FileResponse(final_ogg_path, media_type="audio/ogg")

if __name__ == "__main__":
    uvicorn.run(app, host="0.0.0.0", port=5000)
