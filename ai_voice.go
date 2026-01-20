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
	"time"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/genai"
	"google.golang.org/protobuf/proto" // ✅ Fix for proto functions
)

// ⚙️ SETTINGS
const PY_SERVER = "http://localhost:5000"
const REMOTE_VOICE_URL = "https://voice-real-production.up.railway.app/speak"

// 🎤 MAIN HANDLER
func HandleVoiceMessage(client *whatsmeow.Client, v *events.Message) {
	fmt.Println("🚀 AI Engine: Processing Voice...")

	audioMsg := v.Message.GetAudioMessage()
	if audioMsg == nil {
		return
	}

	senderID := v.Info.Sender.ToNonAD().String()

	// 1. Check Reply Context
	replyContext := ""
	quoted := v.Message.GetExtendedTextMessage().GetContextInfo().GetQuotedMessage()
	if quoted != nil {
		if conversation := quoted.GetConversation(); conversation != "" {
			replyContext = conversation
		} else if imageMsg := quoted.GetImageMessage(); imageMsg != nil {
			replyContext = imageMsg.GetCaption()
		} else if videoMsg := quoted.GetVideoMessage(); videoMsg != nil {
			replyContext = videoMsg.GetCaption()
		}
		if quoted.GetAudioMessage() != nil {
			replyContext = "[User replied to a Voice Note]"
		}
	}

	// ⏳ Status: Recording Audio...
	client.SendChatPresence(context.Background(), v.Info.Chat, types.ChatPresenceComposing, types.ChatPresenceMediaAudio)

	// 2. Download
	data, err := client.Download(context.Background(), audioMsg)
	if err != nil {
		fmt.Println("❌ Download Failed")
		return
	}

	// 3. Transcribe
	userText, err := TranscribeAudio(data)
	if err != nil {
		return
	}
	fmt.Println("🗣️ User Said:", userText)

	if replyContext != "" {
		fmt.Println("🔗 Reply Context Found:", replyContext)
		userText = fmt.Sprintf("(In reply to: '%s') %s", replyContext, userText)
	}

	// 4. Gemini Brain (Short & Natural)
	aiResponse, _ := GetGeminiVoiceResponseWithHistory(userText, senderID)
	if aiResponse == "" {
		return
	}
	fmt.Println("🤖 AI Response:", aiResponse)

	// 5. Generate Voice
	rawAudio, err := GenerateVoice(aiResponse)
	if err != nil || len(rawAudio) == 0 {
		return
	}

	// 6. Convert to OGG Opus (Locally in Go using FFmpeg)
	fmt.Println("🎵 Converting to WhatsApp PTT Format...")
	finalAudio, err := ConvertToOpus(rawAudio)
	if err != nil {
		fmt.Println("❌ FFmpeg Failed, sending raw:", err)
		finalAudio = rawAudio // Fallback
	}

	// 7. Upload & Send
	up, err := client.Upload(context.Background(), finalAudio, whatsmeow.MediaAudio)
	if err != nil {
		return
	}

	_, err = client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		AudioMessage: &waProto.AudioMessage{
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			Mimetype:      proto.String("audio/ogg; codecs=opus"), // ✅ Correct MIME
			FileSHA256:    up.FileSHA256,
			FileEncSHA256: up.FileEncSHA256,
			FileLength:    proto.Uint64(uint64(len(finalAudio))),
			PTT:           proto.Bool(true), // ✅ Blue Mic
		},
	})

	if err == nil && rdb != nil {
		UpdateAIHistory(senderID, userText, aiResponse, "")
		fmt.Println("✅ Voice Note Sent!")
	}
}

// 🎵 FFmpeg Converter (Go Side)
func ConvertToOpus(inputData []byte) ([]byte, error) {
	// Temp files
	inputFile := fmt.Sprintf("temp_in_%d.wav", time.Now().UnixNano())
	outputFile := fmt.Sprintf("temp_out_%d.ogg", time.Now().UnixNano())

	// Write Input
	err := os.WriteFile(inputFile, inputData, 0644)
	if err != nil {
		return nil, err
	}
	defer os.Remove(inputFile)
	defer os.Remove(outputFile)

	// FFmpeg Command (WhatsApp Optimized)
	cmd := exec.Command("ffmpeg", "-y", "-i", inputFile, "-c:a", "libopus", "-b:a", "16k", "-ac", "1", "-f", "ogg", outputFile)
	
	// Hide Output
	cmd.Stdout = nil
	cmd.Stderr = nil

	err = cmd.Run()
	if err != nil {
		return nil, err
	}

	// Read Output
	return os.ReadFile(outputFile)
}

// 🧠 GEMINI LOGIC (Short & Human-Like)
func GetGeminiVoiceResponseWithHistory(query string, senderID string) (string, string) {
	ctx := context.Background()

	// Dynamic API Keys
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
		if len(history) > 1000 { // Keep history short too
			history = history[len(history)-1000:]
		}

		// 🔥🔥🔥 CRITICAL PROMPT: SHORT & HUMAN 🔥🔥🔥
		systemPrompt := fmt.Sprintf(`System: You are a deeply caring, intimate friend.
		
		🔴 CRITICAL RULES:
		1. **SCRIPT:** Output ONLY in **HINDI SCRIPT (Devanagari)**.
		2. **LANGUAGE:** Actual language must be **PURE URDU**. 
		3. **LENGTH (SUPER IMPORTANT):** Keep responses **EXTREMELY SHORT** (10-15 words max).
		   - Act like a real human on chat. Don't write essays.
		   - Just answer directly. No filler words.
		4. **TONE:** Casual, Friendly, Emotional.
		   - Use 'Yaar', 'Jaan'. No 'Janab'.
		   
		Example 1:
		User: "Kya haal hai?"
		You: "मैं ठीक हूँ यार, तुम सुनाओ?" (Short & Sweet)

		Example 2:
		User: "Dil udaas hai."
		You: "अरे क्या हुआ मेरी जान? मुझे बताओ ना।" (Direct & Caring)

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

	return "यार नेट नहीं चल रहा।", ""
}

// 🔌 HELPER: Generate Voice
func GenerateVoice(text string) ([]byte, error) {
	fmt.Println("⚡ Sending Prompt to 32-Core Server...")
	startTime := time.Now()

	audio, err := requestVoiceServer(REMOTE_VOICE_URL, text)
	
	if err != nil {
		fmt.Println("❌ Remote Server Failed, trying Local...", err)
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		writer.WriteField("text", text)
		writer.WriteField("lang", "hi") 
		writer.Close()
		resp, _ := http.Post("http://localhost:5000/speak", writer.FormDataContentType(), body)
		defer resp.Body.Close()
		return io.ReadAll(resp.Body)
	}

	fmt.Printf("🏁 Voice Generated in %v\n", time.Since(startTime))
	return audio, nil
}

// 🔌 Network Helper (Standard)
func requestVoiceServer(url string, text string) ([]byte, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	writer.WriteField("text", text)
	writer.Close()

	// 🔥 INCREASED TIMEOUT TO 10 MINUTES (600 Seconds)
	// Ab yeh 'context deadline exceeded' error nahi dega
	client := http.Client{Timeout: 6000 * time.Second}
	
	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := client.Do(req)
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

// ⏰ SERVER WARMER (Keep-Alive)
func KeepServerAlive() {
    ticker := time.NewTicker(2 * time.Minute) // Har 2 minute baad ping karega
    go func() {
        for range ticker.C {
            // Fake request to keep XTTS loaded in RAM
            http.Get(PY_SERVER) 
            fmt.Println("💓 Ping sent to Python Server to keep it warm!")
        }
    }()
}