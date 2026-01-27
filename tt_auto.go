package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"google.golang.org/protobuf/proto"
)

// 📦 TikTok Search Result Structure
type TTSearchItem struct {
	Title string `json:"title"`
	Url   string `json:"url"`
}

type TTSearchSession struct {
	Results  []TTSearchItem
	SenderID string
}

// 🤖 Auto Status Structure
type AutoStatusConfig struct {
	Enabled   bool
	Tags      string // e.g., "funny"
	LastIndex int    // ٹریک رکھنے کے لیے
}

// 💾 Global Maps (In-Memory Database)
var ttSearchCache = make(map[string]TTSearchSession)   // MessageID -> Results
var autoStatusMap = make(map[string]*AutoStatusConfig) // UserID -> Config

// 🔍 1. TIKTOK SEARCH (.tts query)
// 🔍 1. TIKTOK SEARCH (.tts query)
func handleTTSearch(client *whatsmeow.Client, v *events.Message, query string) {
	if query == "" {
		replyMessage(client, v, "⚠️ *Usage:* .tts funny\n_(Search TikTok Videos)_")
		return
	}

	react(client, v.Info.Chat, v.Info.ID, "🔍")
	fmt.Printf("🚀 [GO] Starting Python Script for query: %s\n", query)

	// Python Script چلائیں
	cmd := exec.Command("python3", "tiktok_nav.py", query)
	
	// آؤٹ پٹ پکڑیں
	output, err := cmd.CombinedOutput()
	
	// 🔥 HARD DEBUG PRINT (Raw Output)
	fmt.Println("---------------------------------------------------")
	fmt.Println("🐍 [PYTHON RAW OUTPUT START]")
	fmt.Println(string(output))
	fmt.Println("🐍 [PYTHON RAW OUTPUT END]")
	fmt.Println("---------------------------------------------------")

	if err != nil {
		fmt.Printf("❌ [GO] Execution Error: %v\n", err)
		replyMessage(client, v, "❌ Search Failed (Script Error). Check Logs.")
		return
	}

	// JSON Parse کرنے کی کوشش کریں
	// کبھی کبھی پائتھون ڈیبگ لاگز بھی پرنٹ کرتا ہے، ہمیں صرف آخری لائن چاہیے ہوتی ہے جو JSON ہو
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	lastLine := lines[len(lines)-1] // ہمیشہ آخری لائن JSON ہوتی ہے

	var results []TTSearchItem
	jsonErr := json.Unmarshal([]byte(lastLine), &results)
	
	if jsonErr != nil {
		fmt.Printf("❌ [GO] JSON Parse Error: %v\n", jsonErr)
		// اگر آخری لائن JSON نہیں تھی تو شاید پورا آؤٹ پٹ ٹرائی کریں
		json.Unmarshal(output, &results)
	}

	if len(results) == 0 {
		replyMessage(client, v, "❌ No results found on TikTok.")
		return
	}

	// کارڈ بنائیں
	menuText := fmt.Sprintf("🎵 *TIKTOK SEARCH: %s*\n\n", strings.ToUpper(query))
	for i, item := range results {
		title := item.Title
		if len(title) > 40 { title = title[:37] + "..." }
		if title == "" { title = "No Caption" }

		menuText += fmt.Sprintf("【 %d 】 %s\n", i+1, title)
	}
	menuText += "\n🔢 *Reply with 1-10 to download.*"

	// مینیو بھیجیں
	resp, err := client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{Text: proto.String(menuText)},
	})

	if err == nil {
		ttSearchCache[resp.ID] = TTSearchSession{
			Results:  results,
			SenderID: v.Info.Sender.User,
		}
		
		go func() {
			time.Sleep(5 * time.Minute)
			delete(ttSearchCache, resp.ID)
		}()
	}
}

// 📥 2. TIKTOK SEARCH REPLY HANDLER
func handleTTSearchReply(client *whatsmeow.Client, v *events.Message, choice string, quotedID string) {
	session, exists := ttSearchCache[quotedID]
	if !exists {
		return
	}

	// Sender Check
	if v.Info.Sender.User != session.SenderID {
		return
	}

	index, err := strconv.Atoi(strings.TrimSpace(choice))
	if err != nil || index < 1 || index > len(session.Results) {
		replyMessage(client, v, "❌ Invalid Number.")
		return
	}

	selectedVideo := session.Results[index-1]

	// ڈاؤن لوڈ شروع
	react(client, v.Info.Chat, v.Info.ID, "⬇️")
	sendPremiumCard(client, v, "TikTok Downloader", "Auto-Engine", "🎬 Downloading: "+selectedVideo.Title)

	// ہمارا پرانا downloadAndSend فنکشن استعمال کریں
	go downloadAndSend(client, v, selectedVideo.Url, "video")

	// مینیو ڈیلیٹ کر دیں (صفائی)
	delete(ttSearchCache, quotedID)
}

// ⚙️ 3. AUTO STATUS SETUP (.ttauto / .ttautoset)
func handleTTAuto(client *whatsmeow.Client, v *events.Message, args []string) {
	senderID := v.Info.Sender.User
	if len(args) == 0 {
		replyMessage(client, v, "⚠️ Usage: .ttauto on | off")
		return
	}

	mode := strings.ToLower(args[0])

	// کنفیگ نکالیں یا نئی بنائیں
	config, exists := autoStatusMap[senderID]
	if !exists {
		config = &AutoStatusConfig{Tags: "funny", Enabled: false}
		autoStatusMap[senderID] = config
	}

	if mode == "on" || mode == "enable" {
		config.Enabled = true
		replyMessage(client, v, fmt.Sprintf("✅ *Auto-Status ENABLED!*\n🏷️ Tag: #%s\n⏳ Bot will upload 5 videos every cycle.", config.Tags))

		// پہلی بار فوراً چلائیں
		go runSingleAutoStatusCheck(client, senderID)

	} else {
		config.Enabled = false
		replyMessage(client, v, "❌ *Auto-Status DISABLED!*")
	}
}

func handleTTAutoSet(client *whatsmeow.Client, v *events.Message, args []string) {
	senderID := v.Info.Sender.User
	if len(args) == 0 {
		replyMessage(client, v, "⚠️ Usage: .ttautoset funny islamic pubg")
		return
	}

	tags := strings.Join(args, " ")

	config, exists := autoStatusMap[senderID]
	if !exists {
		config = &AutoStatusConfig{Enabled: false}
		autoStatusMap[senderID] = config
	}

	config.Tags = tags
	replyMessage(client, v, fmt.Sprintf("✅ *Auto-Tags Updated:*\n🏷️ #%s", tags))
}

// 🔄 4. AUTO STATUS WORKER (Background Loop)
func StartAutoStatusLoop(client *whatsmeow.Client) {
	ticker := time.NewTicker(5 * time.Minute) // ہر 45 منٹ بعد چیک کرے گا
	for range ticker.C {
		for userID, config := range autoStatusMap {
			if config.Enabled {
				go runSingleAutoStatusCheck(client, userID)
			}
		}
	}
}

// 🔄 5. RUN STATUS CHECK (Updated: Posts 5 Random Videos)
func runSingleAutoStatusCheck(client *whatsmeow.Client, userID string) {
	config := autoStatusMap[userID]
	if config == nil || !config.Enabled {
		return
	}

	fmt.Printf("🤖 [AUTO-STATUS] Running for %s | Tag: %s\n", userID, config.Tags)

	// 1. Python سے ویڈیوز کی لسٹ منگوائیں
	cmd := exec.Command("python3", "tiktok_nav.py", "#"+config.Tags)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return
	}

	var results []TTSearchItem
	json.Unmarshal(output, &results)

	// اگر ویڈیوز نہیں ملیں تو واپسی
	if len(results) == 0 {
		return
	}

	// 2. لسٹ کو شفل (Mix) کریں
	rand.Shuffle(len(results), func(i, j int) {
		results[i], results[j] = results[j], results[i]
	})

	// 3. لمٹ سیٹ کریں (5 ویڈیوز)
	limit := 5
	if len(results) < 5 {
		limit = len(results)
	}

	fmt.Printf("📦 [BATCH] Posting %d videos to status...\n", limit)

	// 4. لوپ چلائیں
	for i := 0; i < limit; i++ {
		video := results[i]

		// فائل کا نام یونیک رکھیں
		filename := fmt.Sprintf("autostatus_%s_%d.mp4", userID, time.Now().UnixNano())

		// A. ڈاؤن لوڈ کریں
		dlCmd := exec.Command("yt-dlp", "-o", filename, video.Url)
		if err := dlCmd.Run(); err != nil {
			fmt.Println("❌ Skip: Download failed for", video.Title)
			continue // اگر ایک فیل ہو تو اگلی پر جائیں
		}

		// B. اسٹیٹس پر اپلوڈ کریں
		fileData, err := os.ReadFile(filename)
		if err == nil {
			uploaded, err := client.Upload(context.Background(), fileData, whatsmeow.MediaVideo)
			if err == nil {
				msg := &waProto.Message{
					VideoMessage: &waProto.VideoMessage{
						URL:           proto.String(uploaded.URL),
						DirectPath:    proto.String(uploaded.DirectPath),
						MediaKey:      uploaded.MediaKey,
						Mimetype:      proto.String("video/mp4"),
						FileEncSHA256: uploaded.FileEncSHA256,
						FileSHA256:    uploaded.FileSHA256,
						FileLength:    proto.Uint64(uploaded.FileLength),
						Caption: proto.String(fmt.Sprintf("🤖 Auto Post [%d/5]\n🏷️ #%s\n📝 %s", i+1, config.Tags, video.Title)),
					},
				}

				// ⚡ STATUS JID
				statusJID := types.JID{User: "status", Server: "broadcast"}
				client.SendMessage(context.Background(), statusJID, msg)
				fmt.Printf("✅ [POSTED] Video %d/%d: %s\n", i+1, limit, video.Title)
			}
		}

		// C. صفائی اور وقفہ
		os.Remove(filename)

		// ⚠️ تھوڑا انتظار (15 سیکنڈ)
		if i < limit-1 {
			time.Sleep(15 * time.Second)
		}
	}
}
