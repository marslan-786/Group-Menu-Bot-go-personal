package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
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

// Python Server URL
const PY_SERVER = "http://localhost:5000"

// 🎤 ENTRY POINT: Jab user voice note bhejta hai
func HandleVoiceMessage(client *whatsmeow.Client, v *events.Message) {
	fmt.Println("🚀 AI Engine: Starting Voice Processing...") // LOG 1

	audioMsg := v.Message.GetAudioMessage()
	if audioMsg == nil { return }

	senderID := v.Info.Sender.ToNonAD().String()

	// 🎤 STATUS START
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
	fmt.Println("📥 AI Engine: Downloading Audio...") // LOG 2
	data, err := client.Download(context.Background(), audioMsg)
	if err != nil {
		fmt.Println("❌ Download Failed:", err)
		return
	}

	// 2. Transcribe
	fmt.Println("👂 AI Engine: Transcribing Audio...") // LOG 3
	userText, err := TranscribeAudio(data)
	if err != nil || userText == "" { 
		fmt.Println("❌ Transcribe Failed:", err)
		return 
	}
	fmt.Println("🗣️ User Said:", userText) // LOG 4

	// 3. Gemini Brain
	fmt.Println("🧠 AI Engine: Thinking...") // LOG 5
	aiResponse, _ := GetGeminiVoiceResponseWithHistory(userText, senderID)
	
	if aiResponse == "" { return }
	fmt.Println("🤖 AI Generated:", aiResponse) // LOG 6

	// 4. Generate Audio
	fmt.Println("🎙️ AI Engine: Generating Voice Reply...") // LOG 7
	audioBytes, err := GenerateVoice(aiResponse)
	if err != nil {
		fmt.Println("❌ TTS Failed:", err)
		return
	}

	// 5. Send
	fmt.Println("📤 AI Engine: Uploading Voice Note...") // LOG 8
	up, err := client.Upload(context.Background(), audioBytes, whatsmeow.MediaAudio)
	if err != nil { return }

	resp, err := client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		AudioMessage: &waProto.AudioMessage{
			URL:           PtrString(up.URL),
			DirectPath:    PtrString(up.DirectPath),
			MediaKey:      up.MediaKey,
			Mimetype:      PtrString("audio/ogg; codecs=opus"),
			FileSHA256:    up.FileSHA256,
			FileEncSHA256: up.FileEncSHA256,
			FileLength:    PtrUint64(uint64(len(audioBytes))),
			PTT:           PtrBool(true),
		},
	})

	if err == nil && rdb != nil {
		UpdateAIHistory(senderID, userText, aiResponse, resp.ID)
		fmt.Println("✅ AI Engine: Reply Sent Successfully!") // LOG 9
	}
}

// ... باقی فنکشنز وہی پرانے ہیں ...
// (GetGeminiVoiceResponseWithHistory, UpdateAIHistory, TranscribeAudio, GenerateVoice, PtrString...)
// ان میں کوئی تبدیلی نہیں ہے، بس اوپر والا HandleVoiceMessage ریپلیس کریں۔

// 🧠 GEMINI LOGIC (PERSONA FIX)
func GetGeminiVoiceResponseWithHistory(query string, senderID string) (string, string) {
	ctx := context.Background()
	apiKey := os.Getenv("GOOGLE_API_KEY")
	if apiKey == "" { apiKey = os.Getenv("GOOGLE_API_KEY_1") }

	client, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: apiKey})
	if err != nil {
		log.Println("Gemini Client Error:", err)
		return "یار میرا انٹرنیٹ تھوڑا مسئلہ کر رہا ہے، ایک منٹ رکنا۔", ""
	}

	// 📜 FETCH HISTORY
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
	// Limit history
	if len(history) > 1500 { history = history[len(history)-1500:] }

	// 🔥 THE SOUL PROMPT
	systemPrompt := fmt.Sprintf(`System: You are NOT an AI assistant. You are a very close, deeply caring, and loyal friend to the user.
    
    🔴 YOUR PERSONALITY:
    1. **Role:** Companion & Emotional Support.
    2. **Tone:** Natural Urdu (Casual, Warm). Use 'Yaar', 'Jaan', 'Dost'.
    3. **Prohibited:** NEVER say "How can I help you?".
    4. **Language:** Output strictly in **URDU SCRIPT (Nastaliq)**.
    5. **Length:** Keep responses conversational (1-3 sentences).
    
    📜 Past Conversations:
    %s
    
    👤 User just said (Voice): "%s"`, history, query)

	resp, err := client.Models.GenerateContent(ctx, "gemini-2.5-flash", genai.Text(systemPrompt), nil)
	
	if err != nil {
		log.Println("Gemini Voice Error:", err)
		return "یار نیٹ ورک کا مسئلہ ہے شاید، دوبارہ بولنا؟", ""
	}

	return resp.Text(), ""
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
func GenerateVoice(text string) ([]byte, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	writer.WriteField("text", text)
	writer.WriteField("lang", "ur")
	writer.Close()

	resp, err := http.Post(PY_SERVER+"/speak", writer.FormDataContentType(), body)
	if err != nil { return nil, err }
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API Error: %d - %s", resp.StatusCode, string(bodyBytes))
	}
	return io.ReadAll(resp.Body)
}

// Helpers
func PtrString(s string) *string { return &s }
func PtrBool(b bool) *bool       { return &b }
func PtrUint64(i uint64) *uint64 { return &i }
