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
	"path/filepath"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types/events"
)

// Python Server URL (Internal Docker Network)
const PY_SERVER = "http://localhost:5000"

// 🎤 ENTRY POINT: Jab user voice note bhejta hai
func HandleVoiceMessage(client *whatsmeow.Client, v *events.Message) {
	// 1. Download User's Voice
	audioMsg := v.Message.GetAudioMessage()
	if audioMsg == nil { return }

	data, err := client.Download(context.Background(), audioMsg)
	if err != nil {
		fmt.Println("❌ Download Failed:", err)
		return
	}

	// 2. Send to Python (Ears) -> Get Text
	userText, err := TranscribeAudio(data)
	if err != nil {
		fmt.Println("❌ Transcription Failed:", err)
		return
	}
	
	if userText == "" { return }

	// 3. Send Text to Gemini (Brain) -> Get Response
	aiResponse := GetGeminiResponse(userText, v.Info.Sender.User)
	
	if aiResponse == "" { return }

	// 4. Send Response to Python (Mouth) -> Get Audio
	refVoice := "voices/male_urdu.wav" 
	
	audioBytes, err := GenerateVoice(aiResponse, refVoice)
	if err != nil {
		// Agar voice fail ho jaye to text bhej do
		replyMessage(client, v, aiResponse)
		return
	}

	// 5. Send Audio back to WhatsApp
	up, err := client.Upload(context.Background(), audioBytes, whatsmeow.MediaAudio)
	if err != nil { return }

	// ✅ FIXED: Using helper functions instead of proto.String
	client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		AudioMessage: &waProto.AudioMessage{
			URL:           PtrString(up.URL),
			DirectPath:    PtrString(up.DirectPath),
			MediaKey:      up.MediaKey,
			Mimetype:      PtrString("audio/ogg; codecs=opus"),
			FileSHA256:    up.FileSHA256,
			FileEncSHA256: up.FileEncSHA256,
			FileLength:    PtrUint64(uint64(len(audioBytes))),
			PTT:           PtrBool(true), // Blue Mic (Voice Note)
		},
	})
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

	var result struct {
		Text string `json:"text"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	return result.Text, nil
}

// 🔌 HELPER: Go -> Python (Speak)
func GenerateVoice(text string, refFile string) ([]byte, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	
	writer.WriteField("text", text)
	writer.WriteField("lang", "ur") // Urdu set

	// Load Reference Audio for Cloning
	fileData, err := os.ReadFile(refFile)
	if err != nil { return nil, err }
	
	part, _ := writer.CreateFormFile("speaker_wav", filepath.Base(refFile))
	part.Write(fileData)
	writer.Close()

	resp, err := http.Post(PY_SERVER+"/speak", writer.FormDataContentType(), body)
	if err != nil { return nil, err }
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("API Error: %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

// 🧠 Helper to call Gemini (Copied logic from ai.go, simplified to return string)
func GetGeminiResponse(query, userID string) string {
    // Yahan aap apni ai.go wali logic use kar sakte hain
    // Filhal testing ke liye dummy return kar raha hun:
    return "آپ کا پیغام موصول ہو گیا ہے۔ میں اس پر کام کر رہا ہوں۔"
}

// ✅ HELPER FUNCTIONS (To Fix proto.String Errors)
func PtrString(s string) *string { return &s }
func PtrBool(b bool) *bool       { return &b }
func PtrUint64(i uint64) *uint64 { return &i }
