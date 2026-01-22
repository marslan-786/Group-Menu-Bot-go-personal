package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/genai"
	"google.golang.org/protobuf/proto"
)

// 🧠 1. MAIN AI FUNCTION (Command Handler - Starts Fresh)
func handleAI(client *whatsmeow.Client, v *events.Message, query string, cmd string) {
	if query == "" {
		replyMessage(client, v, "⚠️ Please provide a prompt.")
		return
	}
	// isReply = false (کیونکہ یہ کمانڈ ہے، اس لیے ہسٹری نہیں جائے گی)
	processAIConversation(client, v, query, cmd, false)
}

// 🧠 2. REPLY HANDLER (Continues Conversation)
func handleAIReply(client *whatsmeow.Client, v *events.Message) bool {
	ext := v.Message.GetExtendedTextMessage()
	if ext == nil || ext.ContextInfo == nil || ext.ContextInfo.StanzaID == nil {
		return false
	}

	replyToID := ext.ContextInfo.GetStanzaID()
	senderID := v.Info.Sender.ToNonAD().String()

	if IsReplyToAI(senderID, replyToID) {
		userMsg := v.Message.GetConversation()
		if userMsg == "" {
			userMsg = v.Message.GetExtendedTextMessage().GetText()
		}

		quotedText := ""
		if ext.ContextInfo.QuotedMessage != nil {
			if conv := ext.ContextInfo.QuotedMessage.GetConversation(); conv != "" {
				quotedText = conv
			} else if caption := ext.ContextInfo.QuotedMessage.GetImageMessage().GetCaption(); caption != "" {
				quotedText = caption
			}
		}

		// isReply = true (یہاں ہسٹری ساتھ جائے گی)
		processAIConversation(client, v, userMsg, "ai", true)
		return true
	}
	return false
}

// ⚙️ INTERNAL LOGIC
var (
	currentKeyID = 1
	keyMutex     sync.Mutex
)

// Custom API Response Structure
type CustomAPIResponse struct {
	Response string `json:"response"`
	Status   string `json:"status"`
	Error    string `json:"error"`
}

func getTotalKeysCount() int {
	count := 0
	for {
		keyName := fmt.Sprintf("GOOGLE_API_KEY_%d", count+1)
		if os.Getenv(keyName) == "" {
			break
		}
		count++
	}
	return count
}

// Custom API Call Function (URL Encoded Message)
func callCustomAPI(apiURL string, prompt string) (string, error) {
	encodedPrompt := url.QueryEscape(prompt)
	fullURL := fmt.Sprintf("%s?message=%s", apiURL, encodedPrompt)

	req, err := http.NewRequest("GET", fullURL, nil)
	if err != nil {
		return "", err
	}

	// Browser load time ke liye timeout thora ziyada rakha hai
	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("API returned status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var apiResp CustomAPIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		// Agar JSON parse na ho to shayad raw text ho, usay wapis bhej do
		return string(body), nil
	}

	if apiResp.Status == "failed" || apiResp.Error != "" {
		return "", fmt.Errorf("API Error: %s", apiResp.Error)
	}

	return apiResp.Response, nil
}

func processAIConversation(client *whatsmeow.Client, v *events.Message, query string, cmd string, isReply bool) {
	if !isReply {
		react(client, v.Info.Chat, v.Info.ID, "🧠") // Thinking reaction for new command
	}

	senderID := v.Info.Sender.ToNonAD().String()
	
	// 🔥 HISTORY LOGIC 🔥
	var history string
	if isReply {
		// اگر یہ ریپلائی ہے تو پچھلی ہسٹری نکالو (Database function should limit to 50)
		// NOTE: Apne GetAIHistory function me "LIMIT 50" ki query zaroor check kar lena
		history = GetAIHistory(senderID) 
	} else {
		// اگر کمانڈ ہے تو ہسٹری خالی (Fresh Start)
		history = "No previous context. This is a new conversation starter."
	}

	aiName := "Assistant"
	if strings.ToLower(cmd) == "gpt" {
		aiName = "GPT-4"
	}

	// 🔥🔥🔥 SMART SYSTEM PROMPT 🔥🔥🔥
	// Is prompt me hum ne Language Mirroring aur Length Control set kia hai
	fullPrompt := fmt.Sprintf(
		"System: You are %s.\n"+
			"🔴 **CRITICAL INSTRUCTIONS:**\n"+
			"1. **LANGUAGE MIRRORING (STRICT):**\n"+
			"   - If user speaks **ENGLISH** -> You speak **ENGLISH**.\n"+
			"   - If user speaks **ROMAN URDU** (e.g., 'kya hal hai') -> You speak **ROMAN URDU** (e.g., 'Main theek hun').\n"+
			"   - If user speaks **URDU SCRIPT** (e.g., 'کیا حال ہے') -> You speak **URDU SCRIPT**.\n"+
			"   - NEVER use Hindi/Devanagari script.\n"+
			"2. **RESPONSE LENGTH:**\n"+
			"   - If message is a **Greeting** (Hi, Hello, Salam) -> Keep it **SHORT & SWEET** (1 line).\n"+
			"   - If message is a **Question** -> Provide a detailed helpful answer.\n"+
			"3. **CONTEXT:**\n"+
			"   - Use the provided Chat History ONLY to understand context (e.g., reply-to-reply).\n"+
			"----------------\n"+
			"📜 **CHAT HISTORY (Last 50 msgs):**\n%s\n"+
			"----------------\n"+
			"👤 **USER'S NEW MESSAGE:** %s\n"+
			"🤖 **YOUR RESPONSE:**",
		aiName, history, query)

	ctx := context.Background()
	var finalResponse string
	var lastError error
	var usedSource string = "CustomAPI"

	// =================================================================
	// 🚀 STEP 1: TRY CUSTOM API (Railway - Free Limit Saving)
	// =================================================================
	
	customURL := os.Getenv("CUSTOM_API_URL")
	if customURL == "" {
		customURL = "https://gemini-api-production-b665.up.railway.app/chat"
	}

	// Sirf tab try karo agar prompt length manageable ho (URL limit safe)
	if len(fullPrompt) < 4000 {
		log.Println("🔄 Trying Custom API First...")
		customResp, err := callCustomAPI(customURL, fullPrompt)
		
		if err == nil && customResp != "" {
			finalResponse = customResp
			log.Println("✅ Custom API Success!")
		} else {
			log.Printf("⚠️ Custom API Failed (%v). Switching to Gemini Backup...", err)
			usedSource = "Gemini"
		}
	} else {
		log.Println("⚠️ Prompt too long for GET request. Switching directly to Gemini.")
		usedSource = "Gemini"
	}

	// =================================================================
	// 🚀 STEP 2: FALLBACK TO GEMINI (Key Rotation Loop)
	// =================================================================
	
	if finalResponse == "" {
		totalKeys := getTotalKeysCount()
		if totalKeys == 0 {
			totalKeys = 1
		}

		for i := 0; i < totalKeys; i++ {
			keyMutex.Lock()
			envKeyName := fmt.Sprintf("GOOGLE_API_KEY_%d", currentKeyID)
			apiKey := os.Getenv(envKeyName)
			if apiKey == "" {
				apiKey = os.Getenv("GOOGLE_API_KEY")
			}
			keyMutex.Unlock()

			genaiClient, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: apiKey})
			if err != nil {
				lastError = err
				continue
			}

			// Flash model is faster and cheaper
			result, err := genaiClient.Models.GenerateContent(ctx, "gemini-2.5-flash", genai.Text(fullPrompt), nil)

			if err != nil {
				lastError = err
				log.Printf("❌ Key #%d Failed: %v", currentKeyID, err)
				keyMutex.Lock()
				currentKeyID++
				if currentKeyID > totalKeys {
					currentKeyID = 1
				}
				keyMutex.Unlock()
				time.Sleep(500 * time.Millisecond)
				continue
			}

			finalResponse = result.Text()
			lastError = nil
			break
		}
	}

	// Final Error Handling
	if finalResponse == "" {
		if !isReply {
			errMsg := "❌ System Overload. Please try again later."
			if lastError != nil {
				errMsg += fmt.Sprintf(" (Debug: %v)", lastError)
			}
			replyMessage(client, v, errMsg)
		}
		return
	}

	// Message Send Karna
	respPtr, err := client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{
			Text: proto.String(finalResponse),
			ContextInfo: &waProto.ContextInfo{
				StanzaID:      proto.String(v.Info.ID),
				Participant:   proto.String(v.Info.Sender.String()),
				QuotedMessage: v.Message,
			},
		},
	})

	if err == nil {
		// Save response to DB for future context
		SaveAIHistory(senderID, query, finalResponse, respPtr.ID)
		if !isReply {
			// Reaction based on source used
			if usedSource == "CustomAPI" {
				react(client, v.Info.Chat, v.Info.ID, "⚡") 
			} else {
				react(client, v.Info.Chat, v.Info.ID, "🐢") 
			}
		}
	}
}