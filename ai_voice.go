package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
//	"log"
	"mime/multipart"
	"net/http"
	"os"
	"time"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/genai"
)

const PY_SERVER = "http://localhost:5000"

func HandleVoiceMessage(client *whatsmeow.Client, v *events.Message) {
	fmt.Println("🚀 AI Engine: Starting Voice Processing...")

	audioMsg := v.Message.GetAudioMessage()
	if audioMsg == nil { return }

	senderID := v.Info.Sender.ToNonAD().String()

	stopRecording := make(chan bool)
	go func() {
		client.SendChatPresence(context.Background(), v.Info.Chat, types.ChatPresenceComposing, types.ChatPresenceMediaAudio)
		ticker := time.NewTicker(4 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				client.SendChatPresence(context.Background(), v.Info.Chat, types.ChatPresenceComposing, types.ChatPresenceMediaAudio)
			case <-stopRecording:
				client.SendChatPresence(context.Background(), v.Info.Chat, types.ChatPresencePaused, types.ChatPresenceMediaAudio)
				return
			}
		}
	}()
	defer func() { stopRecording <- true }()

	// 1. Download
	fmt.Println("📥 AI Engine: Downloading Audio...")
	data, err := client.Download(context.Background(), audioMsg)
	if err != nil {
		fmt.Println("❌ Download Failed:", err)
		return
	}

	// 2. Transcribe
	fmt.Println("👂 AI Engine: Transcribing Audio...")
	userText, err := TranscribeAudio(data)
	if err != nil || userText == "" { 
		fmt.Println("❌ Transcribe Failed:", err)
		return 
	}
	fmt.Println("🗣️ User Said:", userText)

	// 3. Gemini Brain
	fmt.Println("🧠 AI Engine: Thinking...")
	aiResponse, _ := GetGeminiVoiceResponseWithHistory(userText, senderID)
	
	if aiResponse == "" { return }
	fmt.Println("🤖 AI Generated:", aiResponse)

	// 4. Generate Audio
	fmt.Println("🎙️ AI Engine: Generating Voice Reply...")
	audioBytes, err := GenerateVoice(aiResponse)
	
	// ✅ SAFETY CHECK: Agar audioBytes khali hai ya error aya, to ruk jao
	if err != nil || len(audioBytes) == 0 {
		fmt.Println("❌ TTS Failed (Empty File):", err)
		return
	}

	// 5. Send
	fmt.Println("📤 AI Engine: Uploading Voice Note...")
	up, err := client.Upload(context.Background(), audioBytes, whatsmeow.MediaAudio)
	if err != nil { return }

	resp, err := client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		AudioMessage: &waProto.AudioMessage{
			URL:           PtrString(up.URL),
			DirectPath:    PtrString(up.DirectPath),
			MediaKey:      up.MediaKey,
			Mimetype:      PtrString("audio/ogg; codecs=opus"), // ✅ Same as handleToPTT
			FileSHA256:    up.FileSHA256,
			FileEncSHA256: up.FileEncSHA256,
			FileLength:    PtrUint64(uint64(len(audioBytes))),
			PTT:           PtrBool(true),
		},
	})

	if err == nil && rdb != nil {
		UpdateAIHistory(senderID, userText, aiResponse, resp.ID)
		fmt.Println("✅ AI Engine: Reply Sent Successfully!")
	}
}

// 🧠 GEMINI LOGIC
// 🧠 GEMINI LOGIC (WITH AUTO KEY ROTATION)
func GetGeminiVoiceResponseWithHistory(query string, senderID string) (string, string) {
    ctx := context.Background()

    // 🔑 1. ساری Keys کی لسٹ بنائیں
    // (یہاں ہم مان رہے ہیں کہ آپ کے پاس ایک سے زیادہ کیز ہیں)
    apiKeys := []string{
        os.Getenv("GOOGLE_API_KEY"),
        os.Getenv("GOOGLE_API_KEY_1"),
        os.Getenv("GOOGLE_API_KEY_2"),
        os.Getenv("GOOGLE_API_KEY_3"),
        os.Getenv("GOOGLE_API_KEY_4"),
        os.Getenv("GOOGLE_API_KEY_5"),
    }

    // خالی Keys نکال دیں
    var validKeys []string
    for _, k := range apiKeys {
        if k != "" {
            validKeys = append(validKeys, k)
        }
    }

    if len(validKeys) == 0 {
        return "سسٹم میں کوئی API Key موجود نہیں ہے۔", ""
    }

    // 🔄 2. RETRY LOOP (سب سے اہم حصہ)
    // یہ لوپ ہر Key کو باری باری ٹرائی کرے گا
    for i := 0; i < len(validKeys); i++ {
        
        // موجودہ Key اٹھائیں
        currentKey := validKeys[i]
        fmt.Printf("🔑 AI Engine: Trying API Key #%d...\n", i+1)

        client, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: currentKey})
        if err != nil {
            fmt.Println("⚠️ Client Error:", err)
            continue // اگلی Key پر جائیں
        }

        // 📜 ہسٹری لائیں (وہی پرانا کوڈ)
        var history string = ""
        if rdb != nil {
            key := "ai_session:" + senderID
            val, err := rdb.Get(ctx, key).Result()
            if err == nil {
                var session AISession
                _ = json.Unmarshal([]byte(val), &session)
                if time.Now().Unix()-session.LastUpdated < 3600 {
                    history = session.History
                }
            }
        }
        if len(history) > 1500 { history = history[len(history)-1500:] }

        // 🔥 PROMPT (وہی پرانا)
        systemPrompt := fmt.Sprintf(`System: You are a very close, deeply caring friend.
        🔴 RULES:
        1. **Format:** Output ONLY in **URDU SCRIPT (Nastaliq)**.
        2. **Tone:** Natural, Casual, Warm (Use 'Yaar', 'Jaan').
        3. **No Emojis:** Do NOT use emojis.
        4. **Length:** Short conversational sentences (1-2 lines).
        
        Chat History: %s
        User Voice: "%s"`, history, query)

        // 🚀 REQUEST (Gemini 2.5 Flash)
        resp, err := client.Models.GenerateContent(ctx, "gemini-2.5-flash", genai.Text(systemPrompt), nil)

        // 🛑 اگر ایرر آئے (Quota یا Overload)
        if err != nil {
            fmt.Printf("❌ Key #%d Failed: %v\n", i+1, err)
            fmt.Println("🔄 Switching to Next Key...")
            continue // ⚠️ یہاں نہیں رکے گا، لوپ دوبارہ چلے گا اگلی Key کے ساتھ
        }

        // ✅ اگر کامیاب ہو جائے
        fmt.Println("✅ Gemini Response Received!")
        return resp.Text(), ""
    }

    // 😭 اگر ساری Keys فیل ہو جائیں
    fmt.Println("❌ ALL API KEYS FAILED!")
    return "یار میرا دماغ ابھی کام نہیں کر رہا، تھوڑی دیر بعد بات کرتے ہیں۔", ""
}


// 💾 HISTORY UPDATER
func UpdateAIHistory(senderID, userQuery, aiResponse, msgID string) {
	ctx := context.Background()
	key := "ai_session:" + senderID
	var history string
	val, err := rdb.Get(ctx, key).Result()
	if err == nil {
		var session AISession
		json.Unmarshal([]byte(val), &session)
		history = session.History
	}
	newHistory := fmt.Sprintf("%s\nUser: %s\nPartner: %s", history, userQuery, aiResponse)
	newSession := AISession{History: newHistory, LastMsgID: msgID, LastUpdated: time.Now().Unix()}
	jsonData, _ := json.Marshal(newSession)
	rdb.Set(ctx, key, jsonData, 60*time.Minute)
}

// 🔌 HELPER: Go -> Python (Transcribe)
func TranscribeAudio(audioData []byte) (string, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "voice.ogg")
	part.Write(audioData)
	writer.Close()

	resp, err := http.Post(PY_SERVER+"/transcribe", writer.FormDataContentType(), body)
	if err != nil { return "", err }
	defer resp.Body.Close()

	var result struct { Text string `json:"text"` }
	json.NewDecoder(resp.Body).Decode(&result)
	return result.Text, nil
}

// 🔌 HELPER: Go -> Python (Speak)
// ⚙️ SETTINGS
// یہاں اپنے نئے پروجیکٹ کا URL ڈالیں جو ریلوے دے گا
const VOICE_API_URL = "https://voice-real-production.up.railway.app/speak"
const USE_REMOTE_VOICE = true // ✅ TRUE = High Quality, FALSE = Local gTTS

// 🔌 HELPER: Generate Voice (Switch Logic)
func GenerateVoice(text string) ([]byte, error) {
	
    // 1️⃣ اگر ریموٹ سرور آن ہے، تو وہاں بھیجیں (High Quality)
    if USE_REMOTE_VOICE {
        fmt.Println("🌍 Sending to Voice Server (Project B)...")
        
        body := &bytes.Buffer{}
        writer := multipart.NewWriter(body)
        writer.WriteField("text", text)
        writer.Close()

        resp, err := http.Post(VOICE_API_URL, writer.FormDataContentType(), body)
        if err != nil {
            fmt.Println("❌ Remote Server Failed, switching to Local:", err)
            // اگر فیل ہو جائے تو نیچے والا لوکل طریقہ چلے گا (Fallback)
        } else {
            defer resp.Body.Close()
            if resp.StatusCode == 200 {
                return io.ReadAll(resp.Body)
            }
            fmt.Println("⚠️ Remote Server Error:", resp.Status)
        }
    }

    // 2️⃣ لوکل طریقہ (gTTS - Backup/Fast)
    fmt.Println("🏠 Generating Locally (gTTS)...")
    body := &bytes.Buffer{}
    writer := multipart.NewWriter(body)
    writer.WriteField("text", text)
    writer.WriteField("lang", "ur") // Local Urdu
    writer.Close()

    // یہ لوکل Python سرور کو کال کرے گا (جو اسی کنٹینر میں ہے)
    resp, err := http.Post("http://localhost:5000/speak", writer.FormDataContentType(), body)
    if err != nil { return nil, err }
    defer resp.Body.Close()
    
    return io.ReadAll(resp.Body)
}

func PtrString(s string) *string { return &s }
func PtrBool(b bool) *bool       { return &b }
func PtrUint64(i uint64) *uint64 { return &i }
