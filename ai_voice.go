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
	"regexp"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/genai"
)

// ⚙️ SETTINGS
const PY_SERVER = "http://localhost:5000"
const USE_REMOTE_VOICE = true // ✅ TRUE = Use High Quality XTTS, FALSE = Local gTTS

// 🚀 VOICE SERVERS LIST (Parallel Processing)
// یہاں اپنے ریلوے کے 3 یا 4 پروجیکٹس کے لنکس ڈالیں
var VoiceServers = []string{
	"https://voice-real-production.up.railway.app/speak", // Project B (Instance 1)
	// "https://voice-2.up.railway.app/speak", // Project C (Instance 2)
	// "https://voice-3.up.railway.app/speak", // Project D (Instance 3)
}

// 🎤 MAIN HANDLER: Voice Message Aaye to ye chalega
func HandleVoiceMessage(client *whatsmeow.Client, v *events.Message) {
	fmt.Println("🚀 AI Engine: Starting Voice Processing...")

	audioMsg := v.Message.GetAudioMessage()
	if audioMsg == nil {
		return
	}

	senderID := v.Info.Sender.ToNonAD().String()

	// ⏳ Typing status dikhane ke liye
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

	// 1. Download Audio
	fmt.Println("📥 AI Engine: Downloading Audio...")
	data, err := client.Download(context.Background(), audioMsg)
	if err != nil {
		fmt.Println("❌ Download Failed:", err)
		return
	}

	// 2. Transcribe (Speech to Text)
	fmt.Println("👂 AI Engine: Transcribing Audio...")
	userText, err := TranscribeAudio(data)
	if err != nil || userText == "" {
		fmt.Println("❌ Transcribe Failed:", err)
		return
	}
	fmt.Println("🗣️ User Said:", userText)

	// 3. Gemini Brain (Thinking)
	fmt.Println("🧠 AI Engine: Thinking...")
	aiResponse, _ := GetGeminiVoiceResponseWithHistory(userText, senderID)

	if aiResponse == "" {
		return
	}
	fmt.Println("🤖 AI Generated:", aiResponse)

	// 4. Generate Voice (Text to Speech - Parallel Mode)
	fmt.Println("🎙️ AI Engine: Generating Voice Reply...")
	audioBytes, err := GenerateVoice(aiResponse)

	// ✅ SAFETY CHECK
	if err != nil || len(audioBytes) == 0 {
		fmt.Println("❌ TTS Failed (Empty File):", err)
		return
	}

	// 5. Upload & Send
	fmt.Println("📤 AI Engine: Uploading Voice Note...")
	up, err := client.Upload(context.Background(), audioBytes, whatsmeow.MediaAudio)
	if err != nil {
		return
	}

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
		fmt.Println("✅ AI Engine: Reply Sent Successfully!")
	}
}

// 🧠 GEMINI LOGIC (WITH AUTO KEY ROTATION)
func GetGeminiVoiceResponseWithHistory(query string, senderID string) (string, string) {
	ctx := context.Background()

	// 🔑 1. ساری Keys کی لسٹ بنائیں
	apiKeys := []string{
		os.Getenv("GOOGLE_API_KEY"),
		os.Getenv("GOOGLE_API_KEY_1"),
		os.Getenv("GOOGLE_API_KEY_2"),
		os.Getenv("GOOGLE_API_KEY_3"),
		os.Getenv("GOOGLE_API_KEY_4"),
		os.Getenv("GOOGLE_API_KEY_5"),
		os.Getenv("GOOGLE_API_KEY_6"),
		os.Getenv("GOOGLE_API_KEY_7"),
		os.Getenv("GOOGLE_API_KEY_9"),
		os.Getenv("GOOGLE_API_KEY_10"),
		os.Getenv("GOOGLE_API_KEY_11"),
		os.Getenv("GOOGLE_API_KEY_12"),
		os.Getenv("GOOGLE_API_KEY_13"),
		os.Getenv("GOOGLE_API_KEY_14"),
		os.Getenv("GOOGLE_API_KEY_15"),
		os.Getenv("GOOGLE_API_KEY_16"),
		os.Getenv("GOOGLE_API_KEY_17"),
		os.Getenv("GOOGLE_API_KEY_18"),
		os.Getenv("GOOGLE_API_KEY_19"),
		os.Getenv("GOOGLE_API_KEY_20"),
		
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

	// 🔄 2. RETRY LOOP
	for i := 0; i < len(validKeys); i++ {

		// موجودہ Key اٹھائیں
		currentKey := validKeys[i]
		fmt.Printf("🔑 AI Engine: Trying API Key #%d...\n", i+1)

		client, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: currentKey})
		if err != nil {
			fmt.Println("⚠️ Client Error:", err)
			continue // اگلی Key پر جائیں
		}

		// 📜 ہسٹری لائیں
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

		// 🔥 PROMPT
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
			continue // ⚠️ یہاں نہیں رکے گا، لوپ دوبارہ چلے گا
		}

		// ✅ اگر کامیاب ہو جائے
		fmt.Println("✅ Gemini Response Received!")
		return resp.Text(), ""
	}

	// 😭 اگر ساری Keys فیل ہو جائیں
	fmt.Println("❌ ALL API KEYS FAILED!")
	return "یار میرا دماغ ابھی کام نہیں کر رہا، تھوڑی دیر بعد بات کرتے ہیں۔", ""
}

// 🔌 HELPER: Generate Voice (SMART PARALLEL SPLITTING)
func GenerateVoice(text string) ([]byte, error) {

	// 1️⃣ REMOTE MODE (High Quality XTTS)
	if USE_REMOTE_VOICE && len(VoiceServers) > 0 {
		fmt.Println("⚡ Starting Parallel Voice Generation (XTTS)...")
		startTime := time.Now()

		// A. جملے توڑیں (Split Text)
		re := regexp.MustCompile(`[۔.?!]+`)
		rawParts := re.Split(text, -1)
		var chunks []string
		for _, s := range rawParts {
			if len(s) > 2 { // خالی ٹکڑے اگنور کریں
				chunks = append(chunks, s)
			}
		}
		if len(chunks) == 0 {
			chunks = []string{text}
		}

		fmt.Printf("📦 Splitting into %d chunks across %d servers...\n", len(chunks), len(VoiceServers))

		// B. Parallel Requests (Goroutines)
		var wg sync.WaitGroup
		audioParts := make(map[int][]byte)
		var mu sync.Mutex

		for i, chunk := range chunks {
			wg.Add(1)

			// Round Robin Load Balancing
			serverIndex := i % len(VoiceServers)
			serverURL := VoiceServers[serverIndex]

			go func(idx int, txt string, url string) {
				defer wg.Done()
				fmt.Printf("🚀 Sending Chunk %d to %s...\n", idx, url)

				audio, err := requestVoiceServer(url, txt)
				if err == nil {
					mu.Lock()
					audioParts[idx] = audio
					mu.Unlock()
					fmt.Printf("✅ Chunk %d Received!\n", idx)
				} else {
					fmt.Printf("❌ Chunk %d Failed: %v\n", idx, err)
				}
			}(i, chunk, serverURL)
		}

		wg.Wait() // سب کا انتظار کریں

		// C. Merge Audio (Stitching)
		var finalAudio []byte
		for i := 0; i < len(chunks); i++ {
			if part, ok := audioParts[i]; ok {
				// ⚠️ WAV HEADER STRIPPING (Crucial for smooth audio)
				// پہلے حصے کا ہیڈر رہنے دیں، باقیوں کا 44 bytes کاٹ دیں
				if i == 0 {
					finalAudio = append(finalAudio, part...)
				} else {
					if len(part) > 44 {
						finalAudio = append(finalAudio, part[44:]...)
					}
				}
			}
		}

		fmt.Printf("🏁 Voice Generated in %v\n", time.Since(startTime))
		return finalAudio, nil
	}

	// 2️⃣ LOCAL FALLBACK (gTTS)
	fmt.Println("🏠 Generating Locally (gTTS Fallback)...")
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	writer.WriteField("text", text)
	writer.WriteField("lang", "ur")
	writer.Close()

	resp, err := http.Post("http://localhost:5000/speak", writer.FormDataContentType(), body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// 🔌 Network Helper
func requestVoiceServer(url string, text string) ([]byte, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	writer.WriteField("text", text)
	writer.Close()

	// 1 Minute Timeout to prevent hanging
	client := http.Client{Timeout: 60 * time.Second}
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

// 🔌 HELPER: Go -> Python (Transcribe)
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

// Helpers
func PtrString(s string) *string { return &s }
func PtrBool(b bool) *bool       { return &b }
func PtrUint64(i uint64) *uint64 { return &i }