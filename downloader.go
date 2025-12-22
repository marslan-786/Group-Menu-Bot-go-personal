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
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types/events"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"google.golang.org/protobuf/proto"
)

// 🛡️ گلوبل اسٹرکچرز


// اگر types.go میں TTState موجود ہے تو اسے یہاں سے ہٹا دیں

var ttCache = make(map[string]TTState)

// 💎 پریمیم کارڈ میکر (ہیلپر)
func sendPremiumCard(client *whatsmeow.Client, v *events.Message, title, site, info string) {
	card := fmt.Sprintf(`╔══════════════════════╗
║ ✨ %s DOWNLOADER
╠══════════════════════╣
║ 📝 Title: %s
║ 🌐 Site: %s
╠══════════════════════╣
║ ⏳ Status: Processing...
╚══════════════════════╝
%s`, strings.ToUpper(site), title, site, info)
	replyMessage(client, v, card)
}

func handleTikTokReply(client *whatsmeow.Client, v *events.Message, input string, senderID string) {
	// 1. کیش سے ڈیٹا نکالیں
	state, exists := ttCache[senderID]
	if !exists { return }

	// 2. ان پٹ چیک کریں
	switch input {
	case "1":
		// ویڈیو بھیجیں
		delete(ttCache, senderID)
		react(client, v.Info.Chat, v.Info.ID, "🎬")
		fmt.Printf("🎬 [TikTok] Sending video to %s\n", senderID)
		sendVideo(client, v, state.PlayURL, "🎬 *TikTok Video*")
		
	case "2":
		// آڈیو بھیجیں
		delete(ttCache, senderID)
		react(client, v.Info.Chat, v.Info.ID, "🎵")
		fmt.Printf("🎵 [TikTok] Sending audio to %s\n", senderID)
		sendDocument(client, v, state.MusicURL, "tiktok_audio.mp3", "audio/mpeg")
		
	case "3":
		// معلومات دکھائیں
		delete(ttCache, senderID)
		infoMsg := fmt.Sprintf(`╔═══════════════════╗
║ 📄 TIKTOK INFO      
╠═══════════════════╣
║ 📝 Title: %s
║ 📊 Size: %.2f MB
║ ✨ Status: Success
╚═══════════════════╝`, state.Title, float64(state.Size)/(1024*1024))
		replyMessage(client, v, infoMsg)
		
	default:
		// اگر 1, 2, 3 کے علاوہ کچھ لکھا تو خاموش رہے یا لاگ کرے
		fmt.Printf("⚠️ [TikTok] Invalid input from %s: %s\n", senderID, input)
	}
}

// 🚀 ہیوی ڈیوٹی میڈیا انجن (The Scientific Power)
func downloadAndSend(client *whatsmeow.Client, v *events.Message, urlStr string, mode string) {
	react(client, v.Info.Chat, v.Info.ID, "⏳")
	
	fileName := fmt.Sprintf("temp_%d", time.Now().UnixNano())
	var args []string

	if mode == "audio" {
		fileName += ".mp3"
		args = []string{"-f", "bestaudio", "--extract-audio", "--audio-format", "mp3", "-o", fileName, urlStr}
	} else {
		fileName += ".mp4"
		args = []string{"-f", "bestvideo[height<=720][ext=mp4]+bestaudio[ext=m4a]/best[ext=mp4]/best", "--merge-output-format", "mp4", "-o", fileName, urlStr}
	}

	// 1. سرور پر رینڈرنگ
	cmd := exec.Command("yt-dlp", args...)
	if err := cmd.Run(); err != nil {
		replyMessage(client, v, "❌ Media processing failed. The link might be broken or private.")
		return
	}

	// 2. بائٹس میں پڑھنا اور اپلوڈ (The Core Logic)
	fileData, err := os.ReadFile(fileName)
	if err != nil { return }
	defer os.Remove(fileName)

	fileSize := uint64(len(fileData))
	mType := whatsmeow.MediaVideo
	if mode == "audio" { mType = whatsmeow.MediaDocument }

	up, err := client.Upload(context.Background(), fileData, mType)
	if err != nil {
		replyMessage(client, v, "❌ WhatsApp upload failed.")
		return
	}

	// 3. پروٹوکول میسج ڈیلیوری
	var finalMsg waProto.Message
	if mode == "audio" {
		finalMsg.DocumentMessage = &waProto.DocumentMessage{
			URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath), MediaKey: up.MediaKey,
			Mimetype: proto.String("audio/mpeg"), FileName: proto.String("Impossible_Audio.mp3"),
			FileLength: proto.Uint64(fileSize), FileSHA256: up.FileSHA256, FileEncSHA256: up.FileEncSHA256,
		}
	} else {
		finalMsg.VideoMessage = &waProto.VideoMessage{
			URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath), MediaKey: up.MediaKey,
			Mimetype: proto.String("video/mp4"), Caption: proto.String("✅ *Downloaded Successfully*"),
			FileLength: proto.Uint64(fileSize), FileSHA256: up.FileSHA256, FileEncSHA256: up.FileEncSHA256,
		}
	}

	client.SendMessage(context.Background(), v.Info.Chat, &finalMsg)
	react(client, v.Info.Chat, v.Info.ID, "✅")
}

// ------------------- تمام ہینڈلرز (بھرے ہوئے!) -------------------

// 📱 سوشل میڈیا
func handleFacebook(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Facebook Video", "Facebook", "🎥 Extracting High Quality Content...")
	go downloadAndSend(client, v, url, "video")
}

func handleInstagram(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Instagram Reel", "Instagram", "📸 Capturing Media...")
	go downloadAndSend(client, v, url, "video")
}

func handleTikTok(client *whatsmeow.Client, v *events.Message, urlStr string) {
	if urlStr == "" { return }
	react(client, v.Info.Chat, v.Info.ID, "🎵")
	apiUrl := "https://www.tikwm.com/api/?url=" + url.QueryEscape(urlStr)
	var r struct { Code int `json:"code"`; Data struct { Play, Music, Title string; Size uint64 } `json:"data"` }
	getJson(apiUrl, &r)
	if r.Code == 0 {
		ttCache[v.Info.Sender.String()] = TTState{PlayURL: r.Data.Play, MusicURL: r.Data.Music, Title: r.Data.Title, Size: int64(r.Data.Size)}
		sendPremiumCard(client, v, "TikTok No-WM", "TikTok", fmt.Sprintf("📝 %s\n\n🔢 Reply 1 for Video | 2 for Audio", r.Data.Title))
	}
}

func handleTwitter(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "X Video", "Twitter/X", "🐦 Speeding through X servers...")
	go downloadAndSend(client, v, url, "video")
}

func handlePinterest(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Pin Media", "Pinterest", "📌 Extracting Media Asset...")
	go downloadAndSend(client, v, url, "video")
}

func handleThreads(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Threads Clip", "Threads", "🧵 Processing Thread...")
	go downloadAndSend(client, v, url, "video")
}

func handleSnapchat(client *whatsmeow.Client, v *events.Message, url string) {
	if url == "" { return }
	react(client, v.Info.Chat, v.Info.ID, "👻")
	sendPremiumCard(client, v, "Snapchat", "Snap-Engine", "👻 Capturing Snap Spotlight... Please wait.")
	
	// سنیپ چیٹ کے لیے ہم مخصوص کوالٹی پیرامیٹرز استعمال کریں گے
	go downloadAndSend(client, v, url, "video")
}

func handleReddit(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Reddit Post", "Reddit", "👽 Merging Audio & Video...")
	go downloadAndSend(client, v, url, "video")
}

// 📺 ویڈیو اور اسٹریمز
func handleYoutubeVideo(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "YouTube HD", "YouTube", "🎬 Fetching 720p/1080p Stream...")
	go downloadAndSend(client, v, url, "video")
}

func handleYoutubeAudio(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "YouTube MP3", "YouTube", "🎶 Converting to 320kbps Audio...")
	go downloadAndSend(client, v, url, "audio")
}

func handleTwitch(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Twitch Clip", "Twitch", "🎮 Grabbing Stream Moment...")
	go downloadAndSend(client, v, url, "video")
}

func handleDailyMotion(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "DailyMotion", "DailyMotion", "📺 Packing Video Stream...")
	go downloadAndSend(client, v, url, "video")
}

func handleVimeo(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Vimeo Pro", "Vimeo", "✨ Professional Extraction...")
	go downloadAndSend(client, v, url, "video")
}

func handleRumble(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Rumble Stream", "Rumble", "🥊 Fetching Rumble Media...")
	go downloadAndSend(client, v, url, "video")
}

func handleBilibili(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Anime Video", "Bilibili", "💮 Accessing Bilibili Nodes...")
	go downloadAndSend(client, v, url, "video")
}

func handleBitChute(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Alt Video", "BitChute", "🎞️ Extraction Started...")
	go downloadAndSend(client, v, url, "video")
}

// 🎵 میوزک پلیٹ فارمز
func handleSoundCloud(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Music Track", "SoundCloud", "🎧 Ripping HQ Audio...")
	go downloadAndSend(client, v, url, "audio")
}

func handleSpotify(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Spotify Track", "Spotify", "🎵 Extracting from Spotify...")
	go downloadAndSend(client, v, url, "audio")
}

func handleAppleMusic(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Apple Preview", "AppleMusic", "🎶 Grabbing High-Fi Clip...")
	go downloadAndSend(client, v, url, "audio")
}

func handleDeezer(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Deezer HQ", "Deezer", "🎼 Converting Track...")
	go downloadAndSend(client, v, url, "audio")
}

func handleTidal(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Tidal Master", "Tidal", "💎 Fetching Lossless Audio...")
	go downloadAndSend(client, v, url, "audio")
}

func handleMixcloud(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "DJ Mixset", "Mixcloud", "🎧 Extracting Long Set...")
	go downloadAndSend(client, v, url, "audio")
}

func handleNapster(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Legacy Track", "Napster", "🎶 Downloading Music...")
	go downloadAndSend(client, v, url, "audio")
}

func handleBandcamp(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Indie Music", "Bandcamp", "🎸 Grabbing Artist Track...")
	go downloadAndSend(client, v, url, "audio")
}

// 🖼️ میڈیا اثاثے
func handleImgur(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Imgur Media", "Imgur", "🖼️ Extracting Image/Video...")
	go downloadAndSend(client, v, url, "video")
}

func handleGiphy(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Animated GIF", "Giphy", "🎞️ Rendering GIF Stream...")
	go downloadAndSend(client, v, url, "video")
}

func handleFlickr(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "HQ Assets", "Flickr", "📸 Fetching Media...")
	go downloadAndSend(client, v, url, "video")
}

func handle9Gag(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Meme Video", "9Gag", "🤣 Grabbing Viral Content...")
	go downloadAndSend(client, v, url, "video")
}

func handleIfunny(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Funny Media", "iFunny", "🤡 Processing Meme...")
	go downloadAndSend(client, v, url, "video")
}

// 💻 ڈویلپر اور آرکائیو
func handleGithub(client *whatsmeow.Client, v *events.Message, urlStr string) {
	react(client, v.Info.Chat, v.Info.ID, "💻")
	// گٹ ہب کے لئے مخصوص لاجک (ڈاؤن لوڈ زپ)
	zipURL := urlStr + "/archive/refs/heads/main.zip"
	sendPremiumCard(client, v, "Repo Source", "GitHub", "📁 Packing Repository ZIP...")
	sendDocument(client, v, zipURL, "Source_Code.zip", "application/zip")
}

func handleArchive(client *whatsmeow.Client, v *events.Message, urlStr string) {
	sendPremiumCard(client, v, "Web Archive", "Archive.org", "🏛️ Fetching Wayback Machine Data...")
	go downloadAndSend(client, v, urlStr, "video")
}

// 📺 یوٹیوب سرچ اور مینو (YTS)
func handleYTS(client *whatsmeow.Client, v *events.Message, query string) {
	if query == "" { return }
	react(client, v.Info.Chat, v.Info.ID, "🔍")
	
	// بوٹ کی کلین آئی ڈی لیں
	myID := getCleanID(client.Store.ID.User)

	cmd := exec.Command("yt-dlp", "ytsearch5:"+query, "--get-title", "--get-id", "--no-playlist")
	out, _ := cmd.Output()
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 { return }

	var results []YTSResult
	// ✨ Bullet Style Design: یہ کبھی نہیں ٹوٹتا
	menuText := "╭─── 📺 *YOUTUBE SEARCH* ───╮\n│\n"
	
	for i := 0; i < len(lines)-1; i += 2 {
		title := lines[i]
		results = append(results, YTSResult{Title: title, Url: "https://www.youtube.com/watch?v=" + lines[i+1]})
		menuText += fmt.Sprintf("📍 *[%d]* %s\n│ ┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈\n", (i/2)+1, title)
	}
	menuText += "│\n╰────────────────────╯"

	resp, err := client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{Text: proto.String(menuText)},
	})

	if err == nil {
		ytCache[resp.ID] = YTSession{Results: results, SenderID: v.Info.Sender.User, BotLID: myID}
		go func() { time.Sleep(2 * time.Minute); delete(ytCache, resp.ID) }()
	}
}

func handleYTDownloadMenu(client *whatsmeow.Client, v *events.Message, ytUrl string) {
	myID := getCleanID(client.Store.ID.User)
	menu := `╔════════════════════╗
║    🎬 VIDEO SELECTOR 
╠════════════════════╣
║ 1️⃣ 360p (Fast)
║ 2️⃣ 720p (HD)
║ 3️⃣ 1080p (FHD)
║ 4️⃣ MP3 (Audio)
║
║ ⏳ Timeout: 1 min
╚════════════════════╝`

	resp, err := client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{Text: proto.String(menu)},
	})

	if err == nil {
		ytDownloadCache[resp.ID] = YTState{Url: ytUrl, BotLID: myID}
		go func() { time.Sleep(1 * time.Minute); delete(ytDownloadCache, resp.ID) }()
	}
}

func handleYTDownload(client *whatsmeow.Client, v *events.Message, ytUrl, format string, isAudio bool) {
	m := "video"; if isAudio { m = "audio" }; go downloadAndSend(client, v, ytUrl, m)
}

// ------------------- مددگار فنکشنز (Helpers) -------------------

func getJson(url string, target interface{}) error {
	r, err := http.Get(url); if err != nil { return err }; defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(target)
}

func sendVideo(client *whatsmeow.Client, v *events.Message, videoURL, caption string) {
	go downloadAndSend(client, v, videoURL, "video")
}

func sendDocument(client *whatsmeow.Client, v *events.Message, docURL, name, mime string) {
	resp, err := http.Get(docURL); if err != nil { return }; defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	up, _ := client.Upload(context.Background(), data, whatsmeow.MediaDocument)
	client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		DocumentMessage: &waProto.DocumentMessage{
			URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath), MediaKey: up.MediaKey,
			Mimetype: proto.String(mime), FileName: proto.String(name), FileLength: proto.Uint64(uint64(len(data))),
		},
	})
}