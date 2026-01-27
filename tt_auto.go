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
	LastIndex int    // ٹریک رکھنے کے لیے کہ کون سی ویڈیو لگائی تھی (optional logic)
}

// 💾 Global Maps (In-Memory Database)
var ttSearchCache = make(map[string]TTSearchSession) // MessageID -> Results
var autoStatusMap = make(map[string]*AutoStatusConfig) // UserID -> Config


// 🔍 1. TIKTOK SEARCH (.tts query)
func handleTTSearch(client *whatsmeow.Client, v *events.Message, query string) {
	if query == "" {
		replyMessage(client, v, "⚠️ *Usage:* .tts funny\n_(Search TikTok Videos)_")
		return
	}

	react(client, v.Info.Chat, v.Info.ID, "🔍")
	replyMessage(client, v, fmt.Sprintf("🔎 *Searching TikTok for:* %s\n_(Please wait extracting 10 videos...)_", query))

	// Python Script چلائیں
	cmd := exec.Command("python3", "tiktok_nav.py", query)
	output, err := cmd.CombinedOutput()
	
	if err != nil {
		fmt.Println("❌ Python Error:", err)
		replyMessage(client, v, "❌ Search Failed (Script Error).")
		return
	}

	// JSON Parse کریں
	var results []TTSearchItem
	err = json.Unmarshal(output, &results)
	if err != nil || len(results) == 0 {
		replyMessage(client, v, "❌ No results found on TikTok.")
		return
	}

	// کارڈ بنائیں
	menuText := fmt.Sprintf("🎵 *TIKTOK SEARCH: %s*\n\n", strings.ToUpper(query))
	for i, item := range results {
		// ٹائٹل کو چھوٹا کریں اگر بہت بڑا ہے
		title := item.Title
		if len(title) > 40 { title = title[:37] + "..." }
		if title == "" { title = "No Caption" }

		menuText += fmt.Sprintf("【 %d 】 %s\n", i+1, title)
	}
	menuText += "\n🔢 *Reply with 1-10 to download.*"

	// مینیو بھیجیں
	resp, _ := client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{Text: proto.String(menuText)},
	})

	// کیش میں محفوظ کریں (تاکہ رپلائی پر ڈاؤن لوڈ کر سکیں)
	if resp != nil {
		ttSearchCache[resp.ID] = TTSearchSession{
			Results:  results,
			SenderID: v.Info.Sender.User,
		}
		
		// 5 منٹ بعد کیش صاف
		go func() {
			time.Sleep(5 * time.Minute)
			delete(ttSearchCache, resp.ID)
		}()
	}
}

// 📥 2. TIKTOK SEARCH REPLY HANDLER
// اسے آپ اپنے main switch case کے default سیکشن میں کال کریں گے جہاں replies ہینڈل ہوتے ہیں
func handleTTSearchReply(client *whatsmeow.Client, v *events.Message, choice string, quotedID string) {
	session, exists := ttSearchCache[quotedID]
	if !exists { return } // اگر کیش میں نہیں ہے تو اگنور

	// Sender Check
	if v.Info.Sender.User != session.SenderID { return }

	index, err := strconv.Atoi(strings.TrimSpace(choice))
	if err != nil || index < 1 || index > len(session.Results) {
		replyMessage(client, v, "❌ Invalid Number.")
		return
	}

	selectedVideo := session.Results[index-1]
	
	// ڈاؤن لوڈ شروع
	react(client, v.Info.Chat, v.Info.ID, "⬇️")
	sendPremiumCard(client, v, "TikTok Downloader", "Auto-Engine", "🎬 Downloading: "+selectedVideo.Title)
	
	// ہمارا پرانا downloadAndSend فنکشن استعمال کریں (یہ yt-dlp کے ذریعے بیسٹ کوالٹی اٹھا لے گا)
	go downloadAndSend(client, v, selectedVideo.Url, "video")
	
	// مینیو ڈیلیٹ کر دیں (صفائی)
	delete(ttSearchCache, quotedID)
}

// ⚙️ 3. AUTO STATUS SETUP (.ttauto / .ttautoset)
func handleTTAuto(client *whatsmeow.Client, v *events.Message, args []string) {
	// صرف اونر کے لیے (اگر چاہیں تو ایڈمن کے لیے بھی کھول دیں)
	// if !isOwner(client, v.Info.Sender) { return }

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
		replyMessage(client, v, fmt.Sprintf("✅ *Auto-Status ENABLED!*\n🏷️ Tag: #%s\n⏳ Bot will upload videos automatically.", config.Tags))
		
		// اگر لوپ نہیں چل رہا تو پہلی بار چلا دیں (یا گلوبل ٹائمر پر چھوڑ دیں)
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
// یہ فنکشن آپ کو main.go میں ایک بار 'go StartAutoStatusLoop(client)' کر کے چلانا ہوگا
func StartAutoStatusLoop(client *whatsmeow.Client) {
	ticker := time.NewTicker(45 * time.Minute) // ہر 45 منٹ بعد چیک کرے گا
	for range ticker.C {
		for userID, config := range autoStatusMap {
			if config.Enabled {
				go runSingleAutoStatusCheck(client, userID)
			}
		}
	}
}

// ایک یوزر کے لیے اسٹیٹس لگانے کا عمل
// ایک یوزر کے لیے اسٹیٹس لگانے کا عمل
func runSingleAutoStatusCheck(client *whatsmeow.Client, userID string) {
	config := autoStatusMap[userID]
	if config == nil || !config.Enabled { return }

	fmt.Printf("🤖 [AUTO-STATUS] Running for %s | Tag: %s\n", userID, config.Tags)

	// 1. Python سے ایک ویڈیو لنک لیں
	cmd := exec.Command("python3", "tiktok_nav.py", "#"+config.Tags)
	output, err := cmd.CombinedOutput()
	if err != nil { return }

	var results []TTSearchItem
	json.Unmarshal(output, &results)
	
	if len(results) == 0 { return }

	// 🛠️ FIX: یہاں سے 'import' والی لائن ہٹا دی گئی ہے
	randomIndex := rand.Intn(len(results))
	video := results[randomIndex]

	// 2. ڈاؤن لوڈ کریں
	filename := fmt.Sprintf("autostatus_%d.mp4", time.Now().Unix())
	
	// yt-dlp کے ذریعے ڈاؤن لوڈ
	dlCmd := exec.Command("yt-dlp", "-o", filename, video.Url)
	if err := dlCmd.Run(); err != nil { return }

	// 3. اسٹیٹس پر اپلوڈ کریں (JID: status@broadcast)
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
					Caption:       proto.String(fmt.Sprintf("🤖 Auto Post: %s\n🏷️ #%s", video.Title, config.Tags)),
				},
			}
			
			// ⚡ STATUS JID
			statusJID := types.JID{User: "status", Server: "broadcast"}
			client.SendMessage(context.Background(), statusJID, msg)
			fmt.Println("✅ [AUTO-STATUS] Posted successfully!")
		}
	}

	// صفائی
	os.Remove(filename)
}
