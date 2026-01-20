package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// ⚙️ SETTINGS
const PY_SERVER = "http://localhost:5000"

// 🚀 VOICE SERVER URL
// چونکہ ہم اب ٹکڑے نہیں کر رہے، تو ایک ہی تگڑا سرور کافی ہے
const REMOTE_VOICE_URL = "https://voice-real-production.up.railway.app/speak"

// 🎤 MAIN HANDLER
func HandleVoiceMessage(client *whatsmeow.Client, v *events.Message) {
	fmt.Println("🚀 AI Engine: Starting Voice Processing (Single Shot Mode)...")

	audioMsg := v.Message.GetAudioMessage()
	if audioMsg == nil {
		return
	}

	senderID := v.Info.Sender.ToNonAD().String()

	// ⏳ Typing/Recording Status (User ko busy rakhne ke liye)
	stopRecording := make(chan bool)
	go func() {
		client.SendChatPresence(context.Background(), v.Info.Chat, types.ChatPresenceComposing, types.ChatPresenceMediaAudio)
		ticker := time.NewTicker(3 * time.Second) // Thora tez ticker
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
	fmt.Println("🧠 AI Engine: Thinking (Hindi Script / Urdu Language)...")
	aiResponse, _ := GetGeminiVoiceResponseWithHistory(userText, senderID)

	if aiResponse == "" {
		return
	}
	// لاگ میں ہندی سکرپٹ چیک کریں
	fmt.Println("🤖 AI Generated (Script):", aiResponse)

	// 4. Generate Voice (FULL SENTENCE - NO CHUNKING)
	fmt.Println("🎙️ AI Engine: Generating Full Voice Reply...")
	audioBytes, err := GenerateVoice(aiResponse)

	// ✅ SAFETY CHECK
	if err != nil || len(audioBytes) == 0 {
		fmt.Println("❌ TTS Failed (Empty File or Error):", err)
		return
	}

	// 5. Send Immediately
	fmt.Printf("📤 AI Engine: Uploading Voice Note (%d bytes)...\n", len(audioBytes))
	up, err := client.Upload(context.Background(), audioBytes, whatsmeow.MediaAudio)
	if err != nil {
		fmt.Println("❌ Upload Failed:", err)
		return
	}

	resp, err := client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		AudioMessage: &waProto.AudioMessage{
			URL:           PtrString(up.URL),
			DirectPath:    PtrString(up.DirectPath),
			MediaKey:      up.MediaKey,
			Mimetype:      PtrString("audio/ogg; codecs=opus"), // WhatsApp Standard
			FileSHA256:    up.FileSHA256,
			FileEncSHA256: up.FileEncSHA256,
			FileLength:    PtrUint64(uint64(len(audioBytes))),
			PTT:           PtrBool(true), // Voice Note Flag
		},
	})

	if err == nil && rdb != nil {
		UpdateAIHistory(senderID, userText, aiResponse, resp.ID)
		fmt.Println("✅ AI Engine: Reply Sent Successfully!")
	} else {
		fmt.Println("❌ Send Failed:", err)
	}
}

// 🧠 GEMINI LOGIC (Modified for Hindi Script / Pure Urdu)
func GetGeminiVoiceResponseWithHistory(query string, senderID string) (string, string) {
	ctx := context.Background()

	// اپنی ساری کیز یہاں ڈال دیں
	apiKeys := []string{
		os.Getenv("GOOGLE_API_KEY"),
		os.Getenv("GOOGLE_API_KEY_1"),
		os.Getenv("GOOGLE_API_KEY_2"),
		os.Getenv("GOOGLE_API_KEY_3"),
		os.Getenv("GOOGLE_API_KEY_4"),
		os.Getenv("GOOGLE_API_KEY_5"),
		os.Getenv("GOOGLE_API_KEY_6"),
		os.Getenv("GOOGLE_API_KEY_7"),
		os.Getenv("GOOGLE_API_KEY_8"),
		os.Getenv("GOOGLE_API_KEY_9"),
		os.Getenv("GOOGLE_API_KEY_10"),
		os.Getenv("GOOGLE_API_KEY_11"),
	}

	var validKeys []string
	for _, k := range apiKeys {
		if k != "" {
			validKeys = append(validKeys, k)
		}
	}

	if len(validKeys) == 0 {
		return "سسٹم میں کوئی API Key موجود نہیں ہے۔", ""
	}

	for i := 0; i < len(validKeys); i++ {
		currentKey := validKeys[i]
		fmt.Printf("🔑 AI Engine: Trying API Key #%d...\n", i+1)

		client, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: currentKey})
		if err != nil {
			fmt.Println("⚠️ Client Error:", err)
			continue
		}

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
		if len(history) > 1500 {
			history = history[len(history)-1500:]
		}

		// 🔥 PROMPT UPDATED FOR HINDI SCRIPT + PURE URDU 🔥
		systemPrompt := fmt.Sprintf(`System: You are a deeply caring, intimate friend.
		
		🔴 CRITICAL INSTRUCTIONS:
		1. **SCRIPT:** Output ONLY in **HINDI SCRIPT (Devanagari)**. Do NOT use Urdu/Arabic script.
		2. **LANGUAGE:** The actual language must be **PURE URDU**. 
		   - Use 'Muhabbat', 'Zindagi', 'Khayal', 'Pareshan'.
		3. **TONE:** Detect emotion. If user is sad, be very soft and comforting. If happy, be cheerful.
		4. **NO ROBOTIC SPEECH:** Speak fluently, like a real human. No formal headers.
		
		Chat History: %s
		User Voice: "%s"`, history, query)

		resp, err := client.Models.GenerateContent(ctx, "gemini-2.5-flash", genai.Text(systemPrompt), nil)

		if err != nil {
			fmt.Printf("❌ Key #%d Failed: %v\n", i+1, err)
			continue
		}

		fmt.Println("✅ Gemini Response Received!")
		return resp.Text(), ""
	}

	return "यार अभी मेरा नेट नहीं चल रहा।", ""
}

// 🔌 HELPER: Generate Voice (DIRECT & FAST)
func GenerateVoice(text string) ([]byte, error) {
	fmt.Println("⚡ Sending Full Prompt to 32-Core Server...")
	startTime := time.Now()

	// ہم سیدھا ایک ہی ریکویسٹ بھیج رہے ہیں (No Chunking)
	// 32 Cores اس کو سیکنڈوں میں ہینڈل کر لیں گے
	audio, err := requestVoiceServer(REMOTE_VOICE_URL, text)
	
	if err != nil {
		fmt.Println("❌ Remote Server Failed, trying Local...", err)
		// Local Fallback (gTTS)
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		writer.WriteField("text", text)
		writer.WriteField("lang", "hi") 
		writer.Close()
		resp, _ := http.Post("http://localhost:5000/speak", writer.FormDataContentType(), body)
		defer resp.Body.Close()
		return io.ReadAll(resp.Body)
	}

	fmt.Printf("🏁 Full Voice Generated in %v\n", time.Since(startTime))
	return audio, nil
}

// 🔌 Network Helper (Standard)
func requestVoiceServer(url string, text string) ([]byte, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	writer.WriteField("text", text)
	writer.Close()

	// ٹائم آؤٹ بڑھا دیا ہے تاکہ بڑی فائل بھی آ سکے
	client := http.Client{Timeout: 300 * time.Second}
	resp, err := client.Post(url, writer.FormDataContentType(), body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("status: %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// 🔌 HELPER: Transcribe
func TranscribeAudio(audioData []byte) (string, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "voice.ogg")
	part.Write(audioData)
	writer.Close()

	resp, err := http.Post(PY_SERVER+"/transcribe", writer.FormDataContentType(), body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct{ Text string `json:"text"` }
	json.NewDecoder(resp.Body).Decode(&result)
	return result.Text, nil
}

// 💾 HISTORY
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

func PtrString(s string) *string { return &s }
func PtrBool(b bool) *bool       { return &b }
func PtrUint64(i uint64) *uint64 { return &i }