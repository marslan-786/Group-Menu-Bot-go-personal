package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types/events"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"google.golang.org/protobuf/proto"
)

// --- 🧠 MEMORY SYSTEM ---
type MovieResult struct {
	Identifier string
	Title      string
	Year       string
	Downloads  int
}

// یوزر کی سرچ ہسٹری محفوظ کرنے کے لیے
var searchCache = make(map[string][]MovieResult)

// ⚠️ ہم نے نام تبدیل کر دیا تاکہ main.go والی cacheMutex سے ٹکراؤ نہ ہو
var movieMutex sync.Mutex 

// Archive API Response Structures
type IAHeader struct {
	Identifier string `json:"identifier"`
	Title      string `json:"title"`
	Year       string `json:"year"`
	Downloads  int    `json:"downloads"`
}
type IAResponse struct {
	Response struct {
		Docs []IAHeader `json:"docs"`
	} `json:"response"`
}
type IAMetadata struct {
	Files []struct {
		Name   string `json:"name"`
		Format string `json:"format"`
		Size   string `json:"size"` 
	} `json:"files"`
}

func handleArchive(client *whatsmeow.Client, v *events.Message, input string) {
	if input == "" { return }
	input = strings.TrimSpace(input)
	senderJID := v.Info.Sender.String()

	// --- 1️⃣ کیا یوزر نے نمبر سلیکٹ کیا ہے؟ (Selection Logic) ---
	if isNumber(input) {
		index, _ := strconv.Atoi(input)
		
		movieMutex.Lock()
		movies, exists := searchCache[senderJID]
		movieMutex.Unlock()

		if exists && index > 0 && index <= len(movies) {
			selectedMovie := movies[index-1]
			// یہاں ہم سلیکٹڈ مووی کو ڈاؤن لوڈ کریں گے
			react(client, v.Info.Chat, v.Info.ID, "💿")
			downloadFromIdentifier(client, v, selectedMovie)
			return
		}
	}

	// --- 2️⃣ کیا یہ ڈائریکٹ لنک ہے؟ (Direct Link Logic) ---
	if strings.HasPrefix(input, "http") {
		react(client, v.Info.Chat, v.Info.ID, "🔗")
		// پریمیم کارڈ ہٹا کر سادہ میسج
		replyMessage(client, v, "⏳ *Processing Direct Link...*")
		downloadFileDirectly(client, v, input, "Unknown_File")
		return
	}

	// --- 3️⃣ یہ سرچ کوئری ہے! (Search Logic) ---
	react(client, v.Info.Chat, v.Info.ID, "🔎")
	go performSearch(client, v, input, senderJID)
}

// --- 🔍 Helper: Search Engine ---
func performSearch(client *whatsmeow.Client, v *events.Message, query string, senderJID string) {
	// Archive Advanced Search API
	encodedQuery := url.QueryEscape(fmt.Sprintf("title:(%s) AND mediatype:(movies)", query))
	apiURL := fmt.Sprintf("https://archive.org/advancedsearch.php?q=%s&fl[]=identifier&fl[]=title&fl[]=year&fl[]=downloads&sort[]=downloads+desc&output=json&rows=10", encodedQuery)

	resp, err := http.Get(apiURL)
	if err != nil {
		replyMessage(client, v, "❌ Search API Error.")
		return
	}
	defer resp.Body.Close()

	var result IAResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		replyMessage(client, v, "❌ Data Parse Error.")
		return
	}

	docs := result.Response.Docs
	if len(docs) == 0 {
		replyMessage(client, v, "🚫 No movies found for: *"+query+"*")
		return
	}

	// میموری میں محفوظ کریں
	var movieList []MovieResult
	msgText := fmt.Sprintf("🎬 *Archive Results for:* '%s'\n\n", query)

	for i, doc := range docs {
		movieList = append(movieList, MovieResult{
			Identifier: doc.Identifier,
			Title:      doc.Title,
			Year:       doc.Year,
			Downloads:  doc.Downloads,
		})
		msgText += fmt.Sprintf("*%d.* %s (%s)\n", i+1, doc.Title, doc.Year)
	}
	
	msgText += "\n👇 *Reply with a number to download.*"

	// گلوبل کیشے اپڈیٹ کریں
	movieMutex.Lock()
	searchCache[senderJID] = movieList
	movieMutex.Unlock()

	// سادہ لسٹ بھیجیں (بغیر پریمیم کارڈ کے)
	client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{
			Text: proto.String(msgText),
			ContextInfo: &waProto.ContextInfo{
				StanzaID:      proto.String(v.Info.ID),
				Participant:   proto.String(v.Info.Sender.String()),
				QuotedMessage: v.Message,
			},
		},
	})
}

// --- 📥 Helper: Find Best Video & Download ---
func downloadFromIdentifier(client *whatsmeow.Client, v *events.Message, movie MovieResult) {
	// Metadata API سے فائلز کی لسٹ لیں
	metaURL := fmt.Sprintf("https://archive.org/metadata/%s", movie.Identifier)
	resp, err := http.Get(metaURL)
	if err != nil { return }
	defer resp.Body.Close()

	var meta IAMetadata
	json.NewDecoder(resp.Body).Decode(&meta)

	bestFile := ""
	maxSize := int64(0)

	for _, f := range meta.Files {
		if strings.HasSuffix(strings.ToLower(f.Name), ".mp4") || strings.HasSuffix(strings.ToLower(f.Name), ".mkv") {
			s, _ := strconv.ParseInt(f.Size, 10, 64)
			if s > maxSize {
				maxSize = s
				bestFile = f.Name
			}
		}
	}

	if bestFile == "" {
		replyMessage(client, v, "❌ No suitable video file found.")
		return
	}

	finalURL := fmt.Sprintf("https://archive.org/download/%s/%s", movie.Identifier, url.PathEscape(bestFile))
	
	// پریمیم کارڈ ہٹا دیا
	replyMessage(client, v, fmt.Sprintf("🚀 *Downloading:* %s\n📦 *Please wait...*", movie.Title))
	
	go downloadFileDirectly(client, v, finalURL, movie.Title)
}

// --- 🚀 Core Downloader ---
func downloadFileDirectly(client *whatsmeow.Client, v *events.Message, urlStr string, customTitle string) {
	req, _ := http.NewRequest("GET", urlStr, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	
	clientHttp := &http.Client{}
	resp, err := clientHttp.Do(req)
	if err != nil || resp.StatusCode != 200 {
		replyMessage(client, v, "❌ Download Failed (Link Invalid).")
		return
	}
	defer resp.Body.Close()

	// نام نکالنا
	fileName := customTitle
	if fileName == "Unknown_File" {
		parts := strings.Split(urlStr, "/")
		fileName = parts[len(parts)-1]
	}
	if !strings.Contains(fileName, ".") { fileName += ".mp4" }

	// Temp File
	tempFile := fmt.Sprintf("temp_%d_%s", time.Now().UnixNano(), fileName)
	out, _ := os.Create(tempFile)
	io.Copy(out, resp.Body)
	out.Close()

	fileData, _ := os.ReadFile(tempFile)
	defer os.Remove(tempFile)

	up, err := client.Upload(context.Background(), fileData, whatsmeow.MediaDocument)
	if err != nil {
		replyMessage(client, v, "❌ Upload Failed.")
		return
	}

	// Send Logic (Simple Video Message)
	client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		DocumentMessage: &waProto.DocumentMessage{
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			Mimetype:      proto.String("video/mp4"),
			Title:         proto.String(fileName),
			FileName:      proto.String(fileName),
			FileLength:    proto.Uint64(uint64(len(fileData))),
			FileSHA256:    up.FileSHA256,
			FileEncSHA256: up.FileEncSHA256,
			Caption:       proto.String("✅ " + fileName),
		},
	})
	react(client, v.Info.Chat, v.Info.ID, "✅")
}

// نوٹ: isNumber, replyMessage, react, sendPremiumCard فنکشنز یہاں سے ڈیلیٹ کر دیے گئے ہیں 
// کیونکہ وہ commands.go اور downloader.go میں پہلے سے موجود ہیں۔
