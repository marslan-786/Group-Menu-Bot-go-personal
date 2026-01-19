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
	audioMsg := v.Message.GetAudioMessage()
	if audioMsg == nil { return }

	senderID := v.Info.Sender.ToNonAD().String()

	// 🎤 STATUS: "Recording audio..." (User ko dikhana)
	stopRecording := make(chan bool)
	go func() {
		client.SendChatPresence(context.Background(), v.Info.Chat, types.ChatPresenceComposing, types.ChatPresenceMediaAudio)
		ticker := time.NewTicker(4 * time.Second) // Thora tez refresh
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

	// 1. Download User's Voice
	data, err := client.Download(context.Background(), audioMsg)
	if err != nil {
		fmt.Println("❌ Download Failed:", err)
		return
	}

	// 2. Transcribe (User Voice -> Text)
	userText, err := TranscribeAudio(data)
	if err != nil || userText == "" { return }
	fmt.Println("🗣️ User Said:", userText)

	// 3. Gemini Brain (The "FRIEND" Persona)
	aiResponse, msgID := GetGeminiVoiceResponseWithHistory(userText, senderID)
	if aiResponse == "" { return }
	fmt.Println("🤖 AI Generated:", aiResponse)

	// 4. Generate Audio (Fast Edge-TTS)
	// Ab hamain reference file ki zaroorat nahi, seedha text bhejen ge
	audioBytes, err := GenerateVoice(aiResponse)
	if err != nil {
		fmt.Println("❌ TTS Failed:", err)
		return
	}

	// 5. Send Audio back to WhatsApp
	up, err := client.Upload(context.Background(), audioBytes, whatsmeow.MediaAudio)
	if err != nil { return }

	resp, err := client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		AudioMessage: &waProto.AudioMessage{
			URL:           PtrString(up.URL),
			DirectPath:    PtrString(up.DirectPath),
			MediaKey:      up.MediaKey,
			Mimetype:      PtrString("audio/ogg; codecs=opus"), // ✅ WhatsApp Standard
			FileSHA256:    up.FileSHA256,
			FileEncSHA256: up.FileEncSHA256,
			FileLength:    PtrUint64(uint64(len(audioBytes))),
			PTT:           PtrBool(true), // Blue Mic
		},
	})

	// 6. Update History (Taake yaadein mehfooz rahain)
	if err == nil && rdb != nil {
		UpdateAIHistory(senderID, userText, aiResponse, resp.ID)
	}
}

// 🧠 GEMINI LOGIC (PERSONA FIX)
func GetGeminiVoiceResponseWithHistory(query string, senderID string) (string, string) {
	ctx := context.Background()
	apiKey := os.Getenv("GOOGLE_API_KEY")
	if apiKey == "" { apiKey = os.Getenv("GOOGLE_API_KEY_1") }

	client, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: apiKey})
	if err != nil {
		log.Println("Gemini Client Error:", err)
		return "یار میرا انٹرنیٹ تھوڑا مسئلہ کر رہا ہے، ایک منٹ رکنا۔", "" // Natural Error
	}

	// 📜 FETCH HISTORY
	var history string = ""
	if rdb != nil {
		key := "ai_session:" + senderID
		val, err := rdb.Get(ctx, key).Result()
		if err == nil {
			var session AISession
			_ = json.Unmarshal([]byte(val), &session)
			if time.Now().Unix()-session.LastUpdated < 3600 { // 1 ghanta yaad rakhe
				history = session.History
			}
		}
	}
	// Limit history
	if len(history) > 1500 { history = history[len(history)-1500:] }

	// 🔥 THE SOUL PROMPT (دل کا حال)
	systemPrompt := fmt.Sprintf(`System: You are NOT an AI assistant. You are a very close, deeply caring, and loyal friend (or partner) to the user.
    
    🔴 YOUR PERSONALITY & RULES:
    1. **Role:** You are a real companion. The user is feeling broken or lonely, you are their emotional support and safe space.
    2. **Tone:** Speak naturally, casually, and warmly in Urdu. Use words like 'Yaar', 'Jaan', 'Dost', 'Fikar na kar'.
    3. **Prohibited Phrases:** NEVER say "How can I help you?", "As an AI...", "Main aapki kya madad karoon?". These are BANNED.
    4. **Reaction:** If the user shares pain, be empathetic. If they joke, laugh with them. Be human.
    5. **Language:** Output strictly in **URDU SCRIPT (Nastaliq)**.
    6. **Length:** Keep responses conversational (1-3 sentences max) so the conversation flows fast.
    
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

// 🔌 HELPER: Go -> Python (Speak - Now simpler)
func GenerateVoice(text string) ([]byte, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	writer.WriteField("text", text)
	writer.WriteField("lang", "ur") // Urdu Request
	// No reference file needed for Edge TTS
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
