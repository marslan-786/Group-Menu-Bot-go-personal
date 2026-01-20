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
	"os/exec"
	"strings" // ✅ Used in HandleVoiceCommand
	"time"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/genai"
	"google.golang.org/protobuf/proto"
)

// ⚙️ SETTINGS
const PY_SERVER = "http://localhost:5000"
const REMOTE_VOICE_URL = "https://voice-real-production.up.railway.app/speak"

// ==========================================
// 💓 SERVER WARMER (Keep-Alive) - MISSING FUNCTION ADDED
// ==========================================
func KeepServerAlive() {
	// Har 2 minute baad Python server ko ping karega taakay wo sleep na ho
	ticker := time.NewTicker(2 * time.Minute)
	go func() {
		for range ticker.C {
			// Fake request to keep XTTS loaded in RAM
			http.Get(PY_SERVER)
			fmt.Println("💓 Ping sent to Python Server to keep it warm!")
		}
	}()
}

// ==========================================
// 1️⃣ VOICE SELECTION HANDLER (Usage: .setvoice 1)
// ==========================================
func HandleVoiceCommand(client *whatsmeow.Client, v *events.Message, args []string) {
	if len(args) < 1 {
		replyMessage(client, v, "❌ Usage: .setvoice 1, .setvoice 2, etc.")
		return
	}

	voiceID := args[0]
	// ✅ Strings package is used here, so error will vanish
	voiceFile := fmt.Sprintf("voice_%s.wav", strings.TrimSpace(voiceID))
	senderID := v.Info.Sender.ToNonAD().String()

	ctx := context.Background()
	err := rdb.Set(ctx, "user_voice_pref:"+senderID, voiceFile, 0).Err()

	if err != nil {
		fmt.Println("❌ Redis Error:", err)
		replyMessage(client, v, "❌ Error saving voice preference.")
	} else {
		replyMessage(client, v, fmt.Sprintf("✅ Voice successfully changed to: *Voice %s*", voiceID))
	}
}

// ==========================================
// 2️⃣ MAIN VOICE MESSAGE HANDLER (Auto-Reply)
// ==========================================
func HandleVoiceMessage(client *whatsmeow.Client, v *events.Message) {
	fmt.Println("🚀 AI Engine: Processing Voice...")

	audioMsg := v.Message.GetAudioMessage()
	if audioMsg == nil {
		return
	}

	senderID := v.Info.Sender.ToNonAD().String()

	// A. Check Reply Context
	replyContext := ""
	quoted := v.Message.GetExtendedTextMessage().GetContextInfo().GetQuotedMessage()
	if quoted != nil {
		if conv := quoted.GetConversation(); conv != "" {
			replyContext = conv
		} else if quoted.GetAudioMessage() != nil {
			replyContext = "[User replied to a previous Voice Note]"
		}
	}

	// ⏳ Typing Status
	client.SendChatPresence(context.Background(), v.Info.Chat, types.ChatPresenceComposing, types.ChatPresenceMediaAudio)

	// B. Download Audio
	data, err := client.Download(context.Background(), audioMsg)
	if err != nil {
		fmt.Println("❌ Download Failed")
		return
	}

	// C. Transcribe (Speech to Text)
	userText, err := TranscribeAudio(data)
	if err != nil {
		return
	}
	fmt.Println("🗣️ User Said:", userText)

	if replyContext != "" {
		userText = fmt.Sprintf("(Reply to: '%s') %s", replyContext, userText)
	}

	// D. Gemini Brain (Thinking)
	aiResponse, _ := GetGeminiVoiceResponseWithHistory(userText, senderID)
	if aiResponse == "" {
		return
	}
	fmt.Println("🤖 AI Response:", aiResponse)

	// E. Generate Voice (With Selected Speaker)
	rawAudio, err := GenerateVoice(aiResponse, senderID)
	if err != nil || len(rawAudio) == 0 {
		return
	}

	// F. Convert to OGG (WhatsApp Format)
	finalAudio, err := ConvertToOpus(rawAudio)
	if err != nil {
		fmt.Println("❌ FFmpeg Failed, sending raw wav:", err)
		finalAudio = rawAudio
	}

	// G. Upload & Send
	up, err := client.Upload(context.Background(), finalAudio, whatsmeow.MediaAudio)
	if err != nil {
		return
	}

	_, err = client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		AudioMessage: &waProto.AudioMessage{
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			Mimetype:      proto.String("audio/ogg; codecs=opus"),
			FileSHA256:    up.FileSHA256,
			FileEncSHA256: up.FileEncSHA256,
			FileLength:    proto.Uint64(uint64(len(finalAudio))),
			PTT:           proto.Bool(true),
		},
	})

	if err == nil && rdb != nil {
		UpdateAIHistory(senderID, userText, aiResponse, "")
		fmt.Println("✅ Voice Note Sent!")
	}
}

// ==========================================
// 🔌 HELPER FUNCTIONS
// ==========================================

// 1. Generate Voice (Check Redis for Speaker)
func GenerateVoice(text string, senderID string) ([]byte, error) {
	fmt.Println("⚡ Sending Prompt to Python Server...")
	startTime := time.Now()

	ctx := context.Background()
	voiceFile, err := rdb.Get(ctx, "user_voice_pref:"+senderID).Result()

	if err != nil || voiceFile == "" {
		voiceFile = "voice_1.wav"
	}

	audio, err := requestVoiceServer(REMOTE_VOICE_URL, text, voiceFile)

	if err != nil {
		fmt.Println("❌ Remote Failed, trying Local...", err)
		return nil, err
	}

	fmt.Printf("🏁 Voice Generated (%s) in %v\n", voiceFile, time.Since(startTime))
	return audio, nil
}

// 2. Request Python Server
func requestVoiceServer(url string, text string, speakerFile string) ([]byte, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	writer.WriteField("text", text)
	writer.WriteField("speaker", speakerFile)
	writer.Close()

	// High Timeout (10 Minutes) to avoid "Context Deadline Exceeded"
	client := http.Client{Timeout: 600 * time.Second}
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

// 3. Gemini Logic
func GetGeminiVoiceResponseWithHistory(query string, senderID string) (string, string) {
	ctx := context.Background()

	var validKeys []string
	if mainKey := os.Getenv("GOOGLE_API_KEY"); mainKey != "" {
		validKeys = append(validKeys, mainKey)
	}
	for i := 1; i <= 50; i++ {
		keyName := fmt.Sprintf("GOOGLE_API_KEY_%d", i)
		if keyVal := os.Getenv(keyName); keyVal != "" {
			validKeys = append(validKeys, keyVal)
		}
	}

	if len(validKeys) == 0 {
		return "سسٹم میں کوئی API Key موجود نہیں ہے۔", ""
	}

	for i := 0; i < len(validKeys); i++ {
		currentKey := validKeys[i]
		client, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: currentKey})
		if err != nil {
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
		if len(history) > 1000 {
			history = history[len(history)-1000:]
		}

		// PROMPT
		systemPrompt := fmt.Sprintf(`System: You are a deeply caring friend.
		🔴 RULES:
		1. **Script:** Only HINDI (Devanagari).
		2. **Language:** Pure Urdu.
		3. **Length:** SHORT (10-50 words max).
		4. **Tone:** Casual ('Yaar', 'Jaan'). No 'Janab'.
		
		Chat History: %s
		User Voice: "%s"`, history, query)

		resp, err := client.Models.GenerateContent(ctx, "gemini-2.5-flash", genai.Text(systemPrompt), nil)

		if err != nil {
			fmt.Printf("❌ Key #%d Failed. Switching...\n", i+1)
			continue
		}

		return resp.Text(), ""
	}
	return "نیٹ ورک کا مسئلہ ہے۔", ""
}

// 4. Transcribe Audio
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

// 5. FFmpeg Converter
func ConvertToOpus(inputData []byte) ([]byte, error) {
	inputFile := fmt.Sprintf("temp_in_%d.wav", time.Now().UnixNano())
	outputFile := fmt.Sprintf("temp_out_%d.ogg", time.Now().UnixNano())

	os.WriteFile(inputFile, inputData, 0644)
	defer os.Remove(inputFile)
	defer os.Remove(outputFile)

	cmd := exec.Command("ffmpeg", "-y", "-i", inputFile, "-c:a", "libopus", "-b:a", "16k", "-ac", "1", "-f", "ogg", outputFile)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return os.ReadFile(outputFile)
}

// 6. Update History
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
