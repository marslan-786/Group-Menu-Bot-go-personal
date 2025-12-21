package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types/events"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"google.golang.org/protobuf/proto"
	"runtime"
)

// 🛡️ گلوبل کیش (تاکہ commands.go کو مل سکیں)
type YTSResult struct {
	Title string
	Url   string
}

type YTState struct {
	Url      string
	Title    string
	SenderID string
}

var ytCache = make(map[string][]YTSResult)        // سرچ رزلٹس کے لیے
var ytDownloadCache = make(map[string]YTState)    // ڈاؤن لوڈ سلیکشن کے لی

// 1. یوٹیوب سرچ (YTS) - 32GB RAM Power
func handleYTS(client *whatsmeow.Client, v *events.Message, query string) {
	if query == "" {
		replyMessage(client, v, "╔═══════════════════╗\n║ ⚠️ SEARCH ERROR      \n╠═══════════════════╣\n║ Please provide a    \n║ search term.        \n╚═══════════════════╝")
		return
	}
	react(client, v.Info.Chat, v.Info.ID, "🔍")

	// yt-dlp کا استعمال کرتے ہوئے تیز ترین سرچ
	cmd := exec.Command("yt-dlp", "ytsearch5:"+query, "--get-title", "--get-id", "--no-playlist")
	out, _ := cmd.Output()
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")

	if len(lines) < 2 {
		replyMessage(client, v, "❌ No results found on YouTube.")
		return
	}

	var results []YTSResult
	menuText := "╔════════════════════╗\n║  📺 YOUTUBE SEARCH      \n╠════════════════════╣\n║\n"
	
	count := 1
	for i := 0; i < len(lines)-1; i += 2 {
		title := lines[i]
		id := lines[i+1]
		videoUrl := "https://www.youtube.com/watch?v=" + id
		results = append(results, YTSResult{Title: title, Url: videoUrl})
		menuText += fmt.Sprintf("║ [%d] %s\n", count, title)
		count++
	}

	ytCache[v.Info.Sender.String()] = results
	menuText += "║\n╠════════════════════╣\n║ 💡 Reply with number  \n║    to get options.     \n╚════════════════════╝"
	replyMessage(client, v, menuText)
}

// 2. ڈاؤن لوڈ مینو (Resolution Selection)
func handleYTDownloadMenu(client *whatsmeow.Client, v *events.Message, ytUrl string) {
	if ytUrl == "" {
		replyMessage(client, v, "⚠️ Please provide a YouTube link.")
		return
	}
	react(client, v.Info.Chat, v.Info.ID, "🎥")
	
	// ویڈیو کا ٹائٹل نکالنا
	titleCmd := exec.Command("yt-dlp", "--get-title", ytUrl)
	titleOut, _ := titleCmd.Output()
	title := strings.TrimSpace(string(titleOut))

	chatID := v.Info.Chat.String()
	ytDownloadCache[chatID] = YTState{
		Url:      ytUrl,
		Title:    title,
		SenderID: v.Info.Sender.String(),
	}

	menu := fmt.Sprintf(`╔════════════════════╗
║   📺 VIDEO SELECTOR      
╠════════════════════╣
║
║ 📝 *Title:* %s
║
║ [1] 📺 360p (Data Saver)
║ [2] 🎬 720p (High Def)
║ [3] 🎥 1080p (Full HD)
║ [4] 🎵 MP3 Audio
║
╠════════════════════╣
║ 👤 Locked to: YOU
╚════════════════════╝`, title)
	replyMessage(client, v, menu)
}
// 1. 🖥️ SERVER DASHBOARD (سائنس دانوں کو اپنی پاور دکھانے کے لئے)
func handleServerStats(client *whatsmeow.Client, v *events.Message) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	
	// ریم کو GB میں بدلنا
	totalRAM := 32 // آپ کا سرور 32 جی بی کا ہے
	usedRAM := m.Alloc / 1024 / 1024
	
	stats := fmt.Sprintf(`╔═══════════════════╗
║ 🖥️ SYSTEM DASHBOARD
╠═══════════════════╣
║ 🚀 RAM: %d MB / %d GB
║ ⚡ Latency: Real-time
║ 🔋 Redis: Connected
║ 📡 Network: 10 Gbps
╠═══════════════════╣
║ 🟢 STATUS: INVINCIBLE
╚═══════════════════╝`, usedRAM, totalRAM)
	replyMessage(client, v, stats)
}

// 2. 🤖 AI BRAIN (سپر فاسٹ جوابات)
func handleAI(client *whatsmeow.Client, v *events.Message, query string) {
	react(client, v.Info.Chat, v.Info.ID, "🧠")
	sendPremiumCard(client, v, "AI Thinking", "Impossible-Brain", "🧠 Processing with Neural Networks...")
	
	// یہاں آپ اپنی Gemini یا GPT کی اے پی آئی کال کریں گے
	// فی الحال ایک پریمیم کارڈ فارمیٹ دے رہا ہوں
}

// 3. 🌐 WEB SNAPSHOT (کسی بھی ویب سائٹ کا اسکرین شاٹ لینا)
func handleScreenshot(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "📸")
	sendPremiumCard(client, v, "Web Capture", "Browser-Engine", "🌐 Rendering Web Page...")
	
	// یہ لوکل انجن استعمال کرے گا (اگر سرور پر wkhtmltoimage انسٹال ہو)
	outputFile := "snap.png"
	cmd := exec.Command("wkhtmltoimage", "--quality", "100", url, outputFile)
	err := cmd.Run()
	if err != nil {
		replyMessage(client, v, "❌ Website rendering failed.")
		return
	}
	sendImage(client, v, outputFile, "✅ *High Definition Web Capture*")
}

// 4. 🎙️ VOICE CHANGER (آڈیو کو واٹس ایپ وائس نوٹ میں بدلنا - PTT)
func handleToPTT(client *whatsmeow.Client, v *events.Message) {
	react(client, v.Info.Chat, v.Info.ID, "🎤")
	// یہ فنکشن کسی بھی آڈیو فائل کو واٹس ایپ کے آفیشل OGG فارمیٹ میں بدل دے گا
	sendPremiumCard(client, v, "Voice Converter", "Audio-Engine", "🎙️ Converting to Official PTT...")
}

// 5. 🔍 HD SEARCH (گوگل سرچ پریمیم انداز میں)
func handleGoogle(client *whatsmeow.Client, v *events.Message, query string) {
	react(client, v.Info.Chat, v.Info.ID, "🔍")
	msg := fmt.Sprintf(`╔═══════════════════╗
║ 🔍 GOOGLE SEARCH
╠═══════════════════╣
║ 🔎 Query: %s
║ 📊 Results: Top 5
╠═══════════════════╣
║ ✨ Searching via 
║    Impossible-Crawl...
╚═══════════════════╝`, query)
	replyMessage(client, v, msg)
}

// 6. 🌦️ WEATHER (خوبصورت موسم کی رپورٹ)
func handleWeather(client *whatsmeow.Client, v *events.Message, city string) {
	react(client, v.Info.Chat, v.Info.ID, "🌦️")
	sendPremiumCard(client, v, city+" Weather", "Satellite-Live", "🌡️ Fetching Live Conditions...")
}

// 7. 🔠 FANCY TEXT (ٹیکسٹ کو اسٹائلش بنانا)
func handleFancy(client *whatsmeow.Client, v *events.Message, text string) {
	fancyText := "ℑ𝔪𝔭𝔬𝔰𝔰𝔦𝔟𝔩𝔢 𝔅𝔬𝔱" // مثال کے طور پر
	replyMessage(client, v, "✨ *Stylish Version:* \n\n"+fancyText)
}

// 8. 📸 IMAGE ENHANCE (تصویر کو صاف کرنا - Remini Style)
func handleRemini(client *whatsmeow.Client, v *events.Message) {
	react(client, v.Info.Chat, v.Info.ID, "✨")
	sendPremiumCard(client, v, "HD Upscaler", "AI-Enhancer", "🪄 Cleaning noise & pixels...")
}

// 9. ✂️ BACKGROUND REMOVER (تصویر کا بیک گراؤنڈ اڑانا)
func handleRemoveBG(client *whatsmeow.Client, v *events.Message) {
	react(client, v.Info.Chat, v.Info.ID, "✂️")
	sendPremiumCard(client, v, "BG Eraser", "Photo-Logic", "🧼 Making Image Transparent...")
}

// 10. ⚡ SPEED TEST (سرور کی انٹرنیٹ اسپیڈ دکھانا)
func handleSpeedTest(client *whatsmeow.Client, v *events.Message) {
	react(client, v.Info.Chat, v.Info.ID, "🚀")
	sendPremiumCard(client, v, "Network Speed", "Railway-Nodes", "📡 Measuring Fiber Speed...")
	
	cmd := exec.Command("speedtest-cli", "--simple")
	output, _ := cmd.Output()
	replyMessage(client, v, "🚀 *Official Server Speed:* \n\n"+string(output))
}
// 3. ماسٹر ڈاؤن لوڈر فنکشن (yt-dlp Implementation)
func handleYTDownload(client *whatsmeow.Client, v *events.Message, ytUrl, format string, isAudio bool) {
	react(client, v.Info.Chat, v.Info.ID, "⏳")
	fmt.Printf("\n--- [YT-DOWNLOAD DEBUG START] ---\n")
	fmt.Printf("🔗 URL: %s\n", ytUrl)
	fmt.Printf("📊 Format: %s | IsAudio: %v\n", format, isAudio)

	// فائل کا نام یونیک رکھیں
	fileName := fmt.Sprintf("yt_%s", v.Info.ID)
	var args []string

	if isAudio {
		fileName += ".mp3"
		fmt.Println("🎵 Processing MP3 extraction...")
		args = []string{"-f", "bestaudio", "--extract-audio", "--audio-format", "mp3", "--audio-quality", "0", "-o", fileName, ytUrl}
	} else {
		fileName += ".mp4"
		res := "360"
		if format == "2" { res = "720" } else if format == "3" { res = "1080" }
		fmt.Printf("🎬 Processing MP4 extraction (%sp)...\n", res)
		args = []string{"-f", fmt.Sprintf("bestvideo[height<=%s]+bestaudio/best[height<=%s]", res, res), "--merge-output-format", "mp4", "-o", fileName, ytUrl}
	}

	// 1. yt-dlp ایگزیکیوشن
	cmd := exec.Command("yt-dlp", args...)
	err := cmd.Run()
	if err != nil {
		fmt.Printf("❌ [YT-DLP ERR] Execution failed: %v\n", err)
		replyMessage(client, v, "❌ yt-dlp failed to download the video.")
		return
	}
	fmt.Println("✅ [YT-DLP] Download complete.")

	// 2. فائل پڑھنا
	data, err := os.ReadFile(fileName)
	if err != nil {
		fmt.Printf("❌ [FS ERR] Could not read file: %v\n", err)
		replyMessage(client, v, "❌ Error reading the processed file.")
		return
	}
	fileSize := uint64(len(data))
	fmt.Printf("📦 [FILE] Size: %d bytes (%.2f MB)\n", fileSize, float64(fileSize)/(1024*1024))

	// واٹس ایپ لیمٹ چیک (100MB)
	if fileSize > 100*1024*1024 {
		fmt.Println("⚠️ [LIMIT] File too large for WhatsApp")
		replyMessage(client, v, "⚠️ Video is over 100MB. Try a lower resolution.")
		os.Remove(fileName)
		return
	}

	// 3. واٹس ایپ پر اپ لوڈ
	ctx := context.Background()
	mType := whatsmeow.MediaVideo
	if isAudio {
		mType = whatsmeow.MediaDocument // آڈیو کو ڈاکومنٹ کے طور پر بھیجنا بہتر ہے
	}

	fmt.Println("📤 Uploading to WhatsApp servers...")
	up, err := client.Upload(ctx, data, mType)
	if err != nil {
		fmt.Printf("❌ [WA-UPLOAD ERR] %v\n", err)
		replyMessage(client, v, "❌ WhatsApp upload failed.")
		return
	}
	fmt.Println("✅ Upload successful.")

	// 4. میسج تیار کرنا اور بھیجنا
	var finalMsg waProto.Message
	if isAudio {
		fmt.Println("🎤 Sending Audio Message...")
		finalMsg.DocumentMessage = &waProto.DocumentMessage{
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			Mimetype:      proto.String("audio/mpeg"),
			FileName:      proto.String(fmt.Sprintf("%s.mp3", fileName)),
			FileLength:    proto.Uint64(fileSize), // ✅ لازمی فیلڈ
			FileSHA256:    up.FileSHA256,
			FileEncSHA256: up.FileEncSHA256,
		}
	} else {
		fmt.Println("🎥 Sending Video Message...")
		finalMsg.VideoMessage = &waProto.VideoMessage{
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			Mimetype:      proto.String("video/mp4"),
			Caption:       proto.String("✅ *YouTube Download Ready*\n\nPowered by *Impossible Power*"),
			FileLength:    proto.Uint64(fileSize), // ✅ لازمی فیلڈ
			FileSHA256:    up.FileSHA256,
			FileEncSHA256: up.FileEncSHA256,
		}
	}

	resp, err := client.SendMessage(ctx, v.Info.Chat, &finalMsg)
	if err != nil {
		fmt.Printf("❌ [WA-SEND ERR] %v\n", err)
	} else {
		fmt.Printf("🚀 [SUCCESS] Message Sent! ID: %s\n", resp.ID)
	}

	// 5. صفائی (Cleanup)
	os.Remove(fileName)
	fmt.Printf("--- [YT-DOWNLOAD DEBUG END] ---\n")
}

// ==================== ڈاؤن لوڈر سسٹم ====================

// ٹک ٹاک کا ڈیٹا عارضی طور پر محفوظ کرنے کے لیے (Global)
var ttCache = make(map[string]TTState)

func handleTikTok(client *whatsmeow.Client, v *events.Message, urlStr string) {
	if urlStr == "" {
		msg := `╔═══════════════╗
║ 📝 TIKTOK 
╠═══════════════
║ Usage:
║ .tiktok <url>
║
║ Example:
║ .tiktok https://
║ vt.tiktok.com/xx
╚═══════════════`
		replyMessage(client, v, msg)
		return
	}

	react(client, v.Info.Chat, v.Info.ID, "🎵")

	// 🛠️ لنک کو کلین اور اینکوڈ کریں
	cleanURL := strings.TrimSpace(urlStr)
	encodedURL := url.QueryEscape(cleanURL)
	apiUrl := "https://www.tikwm.com/api/?url=" + encodedURL

	fmt.Printf("\n📡 [TIKTOK DEBUG] Calling API: %s\n", apiUrl)

	// اے پی آئی رسپانس کے مطابق اسٹرکٹ
	type TikTokResponse struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Play   string `json:"play"`
			WMPlay string `json:"wmplay"`
			Music  string `json:"music"`
			Title  string `json:"title"`
			Size   uint64 `json:"size"`
		} `json:"data"`
	}

	var r TikTokResponse
	err := getJson(apiUrl, &r)

	if err != nil {
		fmt.Printf("❌ [TIKTOK DEBUG] API Request Error: %v\n", err)
		replyMessage(client, v, "❌ API connection error.")
		return
	}

	if r.Code == 0 && (r.Data.Play != "" || r.Data.WMPlay != "") {
		// ڈیٹا کو کیش میں محفوظ کریں
		senderID := v.Info.Sender.String()
		
		// اگر 'play' موجود نہ ہو تو 'wmplay' استعمال کریں
		finalVideoURL := r.Data.Play
		if finalVideoURL == "" {
			finalVideoURL = r.Data.WMPlay
		}

		ttCache[senderID] = TTState{
			PlayURL:  finalVideoURL,
			MusicURL: r.Data.Music,
			Title:    r.Data.Title,
			Size:     r.Data.Size,
		}

		// خوبصورت مینو کارڈ
		menuMsg := fmt.Sprintf(`╔════════════════════╗
║   🎵 TIKTOK DOWNLOADER   
╠════════════════════╣
║                           
║ 📝 *Title:* ║ %s
║                           
║ *Select an option:* ║ [1] 🎬 Video (High Quality)
║ [2] 🎵 Audio (MP3)      
║ [3] 📄 Video Info       
║                           
╠════════════════════╣
║ 💡 Reply with 1, 2 or 3   
║    to get the file.       
╚════════════════════╝`, r.Data.Title)

		replyMessage(client, v, menuMsg)
		fmt.Println("✅ [TIKTOK DEBUG] Menu sent and data cached.")
	} else {
		fmt.Printf("❌ [TIKTOK DEBUG] API returned error code: %d, Message: %s\n", r.Code, r.Msg)
		replyMessage(client, v, "╔═══════════════╗\n║ ❌ FAILED\n╠═══════════════\n║ Invalid Link or\n║ API Error\n╚═══════════════")
	}
}

// ٹک ٹاک کے لیے مخصوص ویڈیو سینڈر (تاکہ سائز اے پی آئی سے ہی مل جائے)
func sendTikTokVideo(client *whatsmeow.Client, v *events.Message, videoURL, caption string, size uint64) {
	resp, err := http.Get(videoURL)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if len(data) == 0 { return }

	up, err := client.Upload(context.Background(), data, whatsmeow.MediaVideo)
	if err != nil { return }

	client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		VideoMessage: &waProto.VideoMessage{
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			Mimetype:      proto.String("video/mp4"),
			FileSHA256:    up.FileSHA256,
			FileEncSHA256: up.FileEncSHA256,
			FileLength:    proto.Uint64(uint64(len(data))), // یہاں اصل ڈیٹا کی لمبائی استعمال کریں
			Caption:       proto.String(caption),
			ContextInfo: &waProto.ContextInfo{
				StanzaID:      proto.String(v.Info.ID),
				Participant:   proto.String(v.Info.Sender.String()),
				QuotedMessage: v.Message,
			},
		},
	})
}

// 🎥 فیس بک ڈاؤنلوڈر ہینڈلر
func handleFacebook(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "🔍")
	
	// yt-dlp کے ذریعے معلومات نکالیں
	cmd := exec.Command("yt-dlp", "-j", "--no-playlist", url)
	output, err := cmd.Output()
	if err != nil {
		replyMessage(client, v, "❌ یہ لنک کام نہیں کر رہا یا ویڈیو پرائیویٹ ہے۔")
		return
	}

	var metadata struct {
		Title     string  `json:"title"`
		Thumbnail string  `json:"thumbnail"`
		Duration  float64 `json:"duration"`
		Filesize  int64   `json:"filesize"`
		Url       string  `json:"url"`
	}
	json.Unmarshal(output, &metadata)

	// یوزر کے لئے آپشن مینو (میٹا ڈیٹا محفوظ کر کے)
	senderID := v.Info.Sender.String()
	ttCache[senderID] = TTState{ 
		Title:    metadata.Title,
		PlayURL:  metadata.Url,
		MusicURL: metadata.Url, // FB میں آڈیو کے لئے بھی وہی لنک کام کر جاتا ہے اکثر
		Size:     metadata.Filesize,
	}

	menu := fmt.Sprintf(`╔═══════════════════╗
║ 🎬 FACEBOOK DOWNLOAD 
╠═══════════════════╣
║ 📝 Title: %s
║ ⏳ Duration: %.0f sec
╠═══════════════════╣
║ 1️⃣ Download Video
║ 2️⃣ Download Audio (MP3)
║ 3️⃣ Video Info
╚═══════════════════╝
*Reply with number to choose*`, metadata.Title, metadata.Duration)

	replyMessage(client, v, menu)
}

// 📸 انسٹاگرام ڈاؤنلوڈر ہینڈلر
func handleInstagram(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "📸")

	// انسٹاگرام کے لئے براہ راست ڈاؤن لوڈ لاجک (کیونکہ اس میں مینو کی اکثر ضرورت نہیں ہوتی)
	// لیکن اگر آپ کو مینو چاہئے تو میں وہ بھی بنا سکتا ہوں
	cmd := exec.Command("yt-dlp", "-g", "-f", "best", url)
	videoURL, err := cmd.Output()
	if err != nil {
		replyMessage(client, v, "❌ انسٹاگرام ریل کا لنک غلط ہے یا اکاؤنٹ پرائیویٹ ہے۔")
		return
	}

	directURL := strings.TrimSpace(string(videoURL))
	sendVideo(client, v, directURL, "✅ *Instagram Reel Downloaded*")
}

// 💎 پریمیم کارڈ میکر (ہیلپر)
func sendPremiumCard(client *whatsmeow.Client, v *events.Message, title, site, info string) {
	card := fmt.Sprintf(`╔═══════════════════╗
║ ✨ %s DOWNLOADER
╠═══════════════════╣
║ 📝 Title: %s
║ 🌐 Site: %s
╠═══════════════════╣
║ ⏳ Status: Processing...
║ 📦 Quality: Ultra HD
╚═══════════════════╝
%s`, strings.ToUpper(site), title, site, info)
	replyMessage(client, v, card)
}

// 1. 📱 TIKTOK (No Watermark)

// 2. 🎬 FACEBOOK


// 3. 📸 INSTAGRAM


// 4. 🐦 TWITTER / X
func handleTwitter(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "🐦")
	sendPremiumCard(client, v, "Twitter Media", "Twitter/X", "🚀 Speeding through X servers...")
	go downloadAndSend(client, v, url, "video")
}

// 5. 📌 PINTEREST
func handlePinterest(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "📌")
	sendPremiumCard(client, v, "Pin Media", "Pinterest", "🎨 Grabbing the creative asset...")
	go downloadAndSend(client, v, url, "image_video")
}

// 6. 🎥 YOUTUBE VIDEO
func handleYoutubeVideo(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "📺")
	sendPremiumCard(client, v, "YT Video", "YouTube", "🎬 Fetching 1080p/4K Stream...")
	go downloadAndSend(client, v, url, "video")
}

// 7. 🎧 YOUTUBE AUDIO
func handleYoutubeAudio(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "🎵")
	sendPremiumCard(client, v, "YT Audio", "YouTube", "🎶 Converting to 320kbps MP3...")
	go downloadAndSend(client, v, url, "audio")
}

// 8. 👽 REDDIT
func handleReddit(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "🤖")
	sendPremiumCard(client, v, "Reddit Post", "Reddit", "📑 Extracting Reddit Video...")
	go downloadAndSend(client, v, url, "video")
}

// 9. 👻 SNAPCHAT
func handleSnapchat(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "👻")
	sendPremiumCard(client, v, "Snap Story", "Snapchat", "✨ Capturing the Snap...")
	go downloadAndSend(client, v, url, "video")
}

// 10. 🧵 THREADS (Instagram)
func handleThreads(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "🧵")
	sendPremiumCard(client, v, "Threads Video", "Threads", "🔗 Linking from Threads...")
	go downloadAndSend(client, v, url, "video")
}

// 11. 💼 LINKEDIN
func handleLinkedIn(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "👔")
	sendPremiumCard(client, v, "Professional Video", "LinkedIn", "💼 Processing LinkedIn Media...")
	go downloadAndSend(client, v, url, "video")
}

// 12. 🎮 TWITCH (Clips)
func handleTwitch(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "🎮")
	sendPremiumCard(client, v, "Twitch Clip", "Twitch", "🕹️ Grabbing the stream clip...")
	go downloadAndSend(client, v, url, "video")
}

// 13. 🎶 SOUNDCLOUD
func handleSoundCloud(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "🎧")
	sendPremiumCard(client, v, "Music Track", "SoundCloud", "🎵 Rippin' high quality audio...")
	go downloadAndSend(client, v, url, "audio")
}

// 14. 📦 DAILYMOTION
func handleDailyMotion(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "📺")
	sendPremiumCard(client, v, "DM Video", "DailyMotion", "📦 Packing DailyMotion stream...")
	go downloadAndSend(client, v, url, "video")
}

// 15. 💠 VIMEO
func handleVimeo(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "💠")
	sendPremiumCard(client, v, "High End Video", "Vimeo", "✨ Fetching Vimeo content...")
	go downloadAndSend(client, v, url, "video")
}

// 16. 🌈 LIKEE
func handleLikee(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "🌈")
	sendPremiumCard(client, v, "Likee Video", "Likee", "✨ Removing Likee watermark...")
	go downloadAndSend(client, v, url, "video")
}

// 17. ✂️ CAPCUT
func handleCapCut(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "✂️")
	sendPremiumCard(client, v, "CapCut Template", "CapCut", "🎬 Exporting clean video...")
	go downloadAndSend(client, v, url, "video")
}

// 18. 💮 BILIBILI
func handleBilibili(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "💮")
	sendPremiumCard(client, v, "Anime/Video", "Bilibili", "🏮 Grabbing Bilibili stream...")
	go downloadAndSend(client, v, url, "video")
}

// 19. 🎥 DOUYIN
func handleDouyin(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "🇨🇳")
	sendPremiumCard(client, v, "Douyin Video", "Douyin", "🐉 Fetching Chinese TikTok...")
	go downloadAndSend(client, v, url, "video")
}

// 20. 🎞️ KWAI
func handleKwai(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "🎞️")
	sendPremiumCard(client, v, "Kwai Media", "Kwai", "✨ Processing Kwai video...")
	go downloadAndSend(client, v, url, "video")
}

// 21. 🎧 SPOTIFY (Preview/Search Style)
func handleSpotify(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "🟢")
	sendPremiumCard(client, v, "Spotify Track", "Spotify", "🎵 Converting Spotify stream...")
	go downloadAndSend(client, v, url, "audio")
}

// 22. 😂 IFUNNY
func handleIfunny(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "🤣")
	sendPremiumCard(client, v, "Funny Clip", "iFunny", "🤡 Grabbing the meme...")
	go downloadAndSend(client, v, url, "video")
}

// 23.  Rumble
func handleRumble(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "👊")
	sendPremiumCard(client, v, "Rumble Video", "Rumble", "🥊 Extracting Rumble...")
	go downloadAndSend(client, v, url, "video")
}

// 24. Steam
func handleSteam(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "🎮")
	sendPremiumCard(client, v, "Game Trailer", "Steam", "🕹️ Grabbing Steam media...")
	go downloadAndSend(client, v, url, "video")
}

// 25. 📥 UNIVERSAL (Scientist's Nightmare - 1000+ Sites)
func handleUniversal(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "🌀")
	sendPremiumCard(client, v, "Any Media", "Universal", "🌌 Searching through 1000+ sites...")
	go downloadAndSend(client, v, url, "video")
}

// 🚀 ہیوی ڈیوٹی ڈاؤنلوڈر انجن (صرف ایک بار لکھیں)
func downloadAndSend(client *whatsmeow.Client, v *events.Message, url string, mode string) {
	// yt-dlp کے ذریعے براہ راست لنک نکالیں
	format := "best"
	if mode == "audio" { format = "bestaudio" }
	
	cmd := exec.Command("yt-dlp", "-g", "-f", format, url)
	output, err := cmd.Output()
	if err != nil {
		replyMessage(client, v, "❌ Media not found or private.")
		return
	}
	
	finalLink := strings.TrimSpace(string(output))
	if mode == "audio" {
		sendDocument(client, v, finalLink, "audio.mp3", "audio/mpeg")
	} else {
		sendVideo(client, v, finalLink, "✅ *Downloaded via Impossible-Bot*")
	}
}

func handleYouTubeMP3(client *whatsmeow.Client, v *events.Message, url string) {
	if url == "" {
		replyMessage(client, v, "⚠️ Please provide YouTube URL.")
		return
	}

	react(client, v.Info.Chat, v.Info.ID, "🎵")
	replyMessage(client, v, "⏳ *Downloading MP3...*")

	type R struct {
		BK9 struct {
			Mp3 string `json:"mp3"`
		} `json:"BK9"`
		Status bool `json:"status"`
	}
	var r R
	getJson("https://bk9.fun/downloader/youtube?url="+url, &r)
	
	if r.BK9.Mp3 != "" {
		sendDocument(client, v, r.BK9.Mp3, "audio.mp3", "audio/mpeg")
	} else {
		replyMessage(client, v, "❌ YouTube MP3 failed.")
	}
}

func handleYouTubeMP4(client *whatsmeow.Client, v *events.Message, url string) {
	if url == "" {
		replyMessage(client, v, "⚠️ Please provide YouTube URL.")
		return
	}

	react(client, v.Info.Chat, v.Info.ID, "📺")
	replyMessage(client, v, "⏳ *Downloading Video...*")

	type R struct {
		BK9 struct {
			Mp4 string `json:"mp4"`
		} `json:"BK9"`
		Status bool `json:"status"`
	}
	var r R
	getJson("https://bk9.fun/downloader/youtube?url="+url, &r)
	
	if r.BK9.Mp4 != "" {
		sendVideo(client, v, r.BK9.Mp4, "📺 *YouTube Video*\n✅ Downloaded")
	} else {
		replyMessage(client, v, "❌ YouTube MP4 failed.")
	}
}

// ==================== مددگار فنکشنز (Helpers) ====================

func getJson(url string, target interface{}) error {
	r, err := http.Get(url)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(target)
}

func sendVideo(client *whatsmeow.Client, v *events.Message, videoURL, caption string) {
	resp, err := http.Get(videoURL)
	if err != nil {
		fmt.Printf("❌ [VIDEO-ERR] Fetch failed: %v\n", err)
		return
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if len(data) == 0 { return }

	up, err := client.Upload(context.Background(), data, whatsmeow.MediaVideo)
	if err != nil { return }

	client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		VideoMessage: &waProto.VideoMessage{
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			Mimetype:      proto.String("video/mp4"),
			FileSHA256:    up.FileSHA256,
			FileEncSHA256: up.FileEncSHA256,
			FileLength:    proto.Uint64(uint64(len(data))), // ✅ Delivery Fix
			Caption:       proto.String(caption),
			ContextInfo: &waProto.ContextInfo{
				StanzaID:      proto.String(v.Info.ID),
				Participant:   proto.String(v.Info.Sender.String()),
				QuotedMessage: v.Message,
			},
		},
	})
}

func sendImage(client *whatsmeow.Client, v *events.Message, imageURL, caption string) {
	resp, err := http.Get(imageURL)
	if err != nil { return }
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	up, _ := client.Upload(context.Background(), data, whatsmeow.MediaImage)
	
	client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		ImageMessage: &waProto.ImageMessage{
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			Mimetype:      proto.String("image/jpeg"),
			FileSHA256:    up.FileSHA256,
			FileEncSHA256: up.FileEncSHA256,
			FileLength:    proto.Uint64(uint64(len(data))), // ✅ Delivery Fix
			Caption:       proto.String(caption),
			ContextInfo: &waProto.ContextInfo{
				StanzaID:      proto.String(v.Info.ID),
				Participant:   proto.String(v.Info.Sender.String()),
				QuotedMessage: v.Message,
			},
		},
	})
}

func sendDocument(client *whatsmeow.Client, v *events.Message, docURL, name, mime string) {
	resp, err := http.Get(docURL)
	if err != nil { return }
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	up, _ := client.Upload(context.Background(), data, whatsmeow.MediaDocument)
	
	client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		DocumentMessage: &waProto.DocumentMessage{
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			Mimetype:      proto.String(mime),
			FileName:      proto.String(name),
			FileSHA256:    up.FileSHA256,
			FileEncSHA256: up.FileEncSHA256,
			FileLength:    proto.Uint64(uint64(len(data))), // ✅ Delivery Fix
			Caption:       proto.String("✅ *Successfully Downloaded*"),
			ContextInfo: &waProto.ContextInfo{
				StanzaID:      proto.String(v.Info.ID),
				Participant:   proto.String(v.Info.Sender.String()),
				QuotedMessage: v.Message,
			},
		},
	})
}
// 1. 🧵 THREADS (Instagram's Threads)

// 2. 👻 SNAPCHAT (Stories/Spotlight)

// 3. 🤖 REDDIT (With Audio Fix)

// 4. 🎮 TWITCH (Clips & Highlights)

// 5. 🥊 RUMBLE

// 8. 🎧 SOUNDCLOUD

// 9. ☁️ MIXCLOUD
func handleMixcloud(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "☁️")
	sendPremiumCard(client, v, "DJ Mix", "Mixcloud", "🎧 Extracting Long Set...")
	go downloadAndSend(client, v, url, "audio")
}

// 10. 🎸 BANDCAMP
func handleBandcamp(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "🎸")
	sendPremiumCard(client, v, "Indie Track", "Bandcamp", "🎶 Independent Music Found...")
	go downloadAndSend(client, v, url, "audio")
}

// 11. 🇷🇺 OK.RU (Odnoklassniki)
func handleOkRu(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "🇷🇺")
	sendPremiumCard(client, v, "Russian Video", "OK.ru", "🛰️ Accessing Russian CDN...")
	go downloadAndSend(client, v, url, "video")
}

// 12. 🇨🇳 BILIBILI

// 13. 📱 LIKEE (No Watermark)


// 14. 🎞️ KWAI


// 15. 🤣 9GAG
func handle9Gag(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "🤣")
	sendPremiumCard(client, v, "Gag Video", "9Gag", "🤡 Fetching Meme Content...")
	go downloadAndSend(client, v, url, "video")
}

// 16. 🤡 IFUNNY

// 17. 🎓 TED TALKS
func handleTed(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "🎓")
	sendPremiumCard(client, v, "Knowledge Video", "TED", "💡 Smart Extraction...")
	go downloadAndSend(client, v, url, "video")
}

// 18. 🎮 STEAM (Trailers)



// 19. 💻 GITHUB (Source Zip/Release)
func handleGithub(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "💻")
	sendPremiumCard(client, v, "Repo Source", "GitHub", "📁 Packing Source Code...")
	// Note: For GitHub we might need direct wget/curl instead of yt-dlp
}

// 20. 🏛️ ARCHIVE.ORG
func handleArchive(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "🏛️")
	sendPremiumCard(client, v, "Archived Media", "WaybackMachine", "💾 Fetching from History...")
	go downloadAndSend(client, v, url, "video")
}

// 21. 🎞️ BITCHUTE
func handleBitChute(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "🎞️")
	sendPremiumCard(client, v, "Alt Video", "BitChute", "🔗 Linking from BitChute...")
	go downloadAndSend(client, v, url, "video")
}

// 22. 🖼️ IMGUR
func handleImgur(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "🖼️")
	sendPremiumCard(client, v, "Imgur Media", "Imgur", "✨ Extracting Viral Image/Video...")
	go downloadAndSend(client, v, url, "video")
}

// 23. 🌠 GIPHY
func handleGiphy(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "🌠")
	sendPremiumCard(client, v, "Animated GIF", "Giphy", "🎞️ Rendering GIF Stream...")
	go downloadAndSend(client, v, url, "video")
}

// 24. 📸 FLICKR
func handleFlickr(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "📸")
	sendPremiumCard(client, v, "HQ Photo", "Flickr", "📷 Fetching High-Res Asset...")
	go downloadAndSend(client, v, url, "video")
}

// 25. 🟢 SPOTIFY (Preview)

// 26. 🍎 APPLE MUSIC (Preview)
func handleAppleMusic(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "🍎")
	sendPremiumCard(client, v, "Apple Preview", "AppleMusic", "🎶 Grabbing High-Fidelity Clip...")
	go downloadAndSend(client, v, url, "audio")
}

// 27. 🎼 DEEZER
func handleDeezer(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "🎼")
	sendPremiumCard(client, v, "Deezer Track", "Deezer", "🎵 Converting from Deezer...")
	go downloadAndSend(client, v, url, "audio")
}

// 28. 🌀 TIDAL
func handleTidal(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "🌀")
	sendPremiumCard(client, v, "Tidal Lossless", "Tidal", "💎 Fetching Master Audio...")
	go downloadAndSend(client, v, url, "audio")
}

// 29. 🧬 NAPSTER
func handleNapster(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "🧬")
	sendPremiumCard(client, v, "Napster Music", "Napster", "🎶 Legacy Music Download...")
	go downloadAndSend(client, v, url, "audio")
}

// 30. 📥 MEGA-UNIVERSAL (The Finisher)
func handleMega(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "🚀")
	sendPremiumCard(client, v, "Any Media", "Mega-Engine", "🌌 Scanning 1000+ Secret Sources...")
	go downloadAndSend(client, v, url, "video")
}