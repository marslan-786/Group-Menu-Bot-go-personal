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

// 3. ماسٹر ڈاؤن لوڈر فنکشن (yt-dlp Implementation)
func handleYTDownload(client *whatsmeow.Client, v *events.Message, ytUrl, format string, isAudio bool) {
	react(client, v.Info.Chat, v.Info.ID, "⏳")
	
	// فائل کا نام یونیک رکھیں تاکہ کنفلکٹ نہ ہو
	fileName := fmt.Sprintf("dl_%d_%s", os.Getpid(), v.Info.ID)
	var args []string

	if isAudio {
		fileName += ".mp3"
		args = []string{"-f", "bestaudio", "--extract-audio", "--audio-format", "mp3", "-o", fileName, ytUrl}
	} else {
		fileName += ".mp4"
		// ریزولوشن لاجک
		res := "360"
		if format == "2" { res = "720" } else if format == "3" { res = "1080" }
		args = []string{"-f", fmt.Sprintf("bestvideo[height<=%s]+bestaudio/best[height<=%s]", res, res), "--merge-output-format", "mp4", "-o", fileName, ytUrl}
	}

	// 🚀 32GB RAM کا فائدہ: لوکل پروسیسنگ
	cmd := exec.Command("yt-dlp", args...)
	err := cmd.Run()
	if err != nil {
		fmt.Printf("❌ [YT-DLP ERR] %v\n", err)
		replyMessage(client, v, "❌ Failed to process media. Link might be restricted.")
		return
	}

	data, err := os.ReadFile(fileName)
	if err != nil || len(data) == 0 {
		replyMessage(client, v, "❌ Error reading downloaded file.")
		return
	}

	// فائل سائز چیک کریں (WhatsApp Limit)
	if len(data) > 100*1024*1024 { // 100MB
		replyMessage(client, v, "⚠️ File is too large to send via WhatsApp.")
		os.Remove(fileName)
		return
	}

	if isAudio {
		sendDocument(client, v, "", fileName, "audio/mpeg") // آپ کا موجودہ ڈاکومنٹ سینڈر
	} else {
		up, err := client.Upload(context.Background(), data, whatsmeow.MediaVideo)
		if err != nil { return }

		client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
			VideoMessage: &waProto.VideoMessage{
				URL:        proto.String(up.URL),
				DirectPath: proto.String(up.DirectPath),
				MediaKey:   up.MediaKey,
				Mimetype:   proto.String("video/mp4"),
				FileLength: proto.Uint64(uint64(len(data))), // ڈیلیوری فکس
				Caption:    proto.String("✅ Successfully Downloaded via *Impossible Power*"),
			},
		})
	}
	
	// صفائی (Cleanup)
	os.Remove(fileName)
}

// ==================== ڈاؤن لوڈر سسٹم ====================

// ٹک ٹاک کا ڈیٹا عارضی طور پر محفوظ کرنے کے لیے (Global)
var ttCache = make(map[string]TTState)

type TTState struct {
	PlayURL  string
	MusicURL string
	Title    string
	Size     uint64
}

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

func handleFacebook(client *whatsmeow.Client, v *events.Message, url string) {
	if url == "" {
		msg := `╔═══════════════╗
║ 📘 FACEBOOK
╠═══════════════
║ Usage:
║ .fb <url>
║
║ Example:
║ .fb https://
║ fb.watch/xxxx
╚═══════════════`
		replyMessage(client, v, msg)
		return
	}

	react(client, v.Info.Chat, v.Info.ID, "📘")
	
	msg := `╔═══════════════╗
║ 📘 PROCESSING
╠═══════════════
║ ⏳ Downloading
║ Please wait...
╚═══════════════`
	replyMessage(client, v, msg)

	type R struct {
		BK9 struct {
			HD string `json:"HD"`
		} `json:"BK9"`
		Status bool `json:"status"`
	}
	var r R
	err := getJson("https://bk9.fun/downloader/facebook?url="+url, &r)
	
	if err == nil && r.BK9.HD != "" {
		sendVideo(client, v, r.BK9.HD, "📘 *Facebook Video*\n✅ Successfully Downloaded")
	} else {
		replyMessage(client, v, "╔═══════════════╗\n║ ❌ FAILED\n╠═══════════════\n║ Could not fetch\n║ video. Try HD.\n╚═══════════════")
	}
}

func handleInstagram(client *whatsmeow.Client, v *events.Message, url string) {
	if url == "" {
		msg := `╔═══════════════╗
║ 📸 INSTAGRAM
╠═══════════════
║ Usage:
║ .ig <url>
║
║ Example:
║ .ig https://
║ instagram.com/
║ p/xxxxx
╚═══════════════`
		replyMessage(client, v, msg)
		return
	}

	react(client, v.Info.Chat, v.Info.ID, "📸")
	
	msg := `╔═══════════════╗
║ 📸 PROCESSING
╠═══════════════
║ ⏳ Downloading
║ Please wait...
╚═══════════════`
	replyMessage(client, v, msg)

	type R struct {
		Data []struct {
			Url string `json:"url"`
		} `json:"data"`
	}
	var r R
	err := getJson("https://bk9.fun/downloader/instagram?url="+url, &r)
	
	if err == nil && len(r.Data) > 0 {
		sendVideo(client, v, r.Data[0].Url, "📸 *Instagram Video*\n✅ Successfully Downloaded")
	} else {
		replyMessage(client, v, "╔═══════════════╗\n║ ❌ FAILED\n╠═══════════════\n║ Private account\n║ or invalid link.\n╚═══════════════")
	}
}

func handlePinterest(client *whatsmeow.Client, v *events.Message, url string) {
	if url == "" {
		msg := `╔═══════════════╗
║ 📌 PINTEREST
╠═══════════════
║ Usage:
║ .pin <url>
╚═══════════════`
		replyMessage(client, v, msg)
		return
	}

	react(client, v.Info.Chat, v.Info.ID, "📌")
	
	msg := `╔═══════════════╗
║ 📌 PROCESSING
╠═══════════════
║ ⏳ Downloading
╚═══════════════`
	replyMessage(client, v, msg)

	type R struct {
		BK9    string `json:"BK9"`
		Status bool   `json:"status"`
	}
	var r R
	getJson("https://bk9.fun/downloader/pinterest?url="+url, &r)
	
	if r.BK9 != "" {
		sendImage(client, v, r.BK9, "📌 *Pinterest Image*\n✅ Downloaded")
	} else {
		replyMessage(client, v, "❌ Pinterest download failed.")
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