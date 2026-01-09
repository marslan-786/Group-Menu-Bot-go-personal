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

var searchCache = make(map[string][]MovieResult)
var movieMutex sync.Mutex 

// Archive API Response Structures
type IAHeader struct {
	Identifier string      `json:"identifier"`
	Title      string      `json:"title"`
	Year       interface{} `json:"year"`
	Downloads  interface{} `json:"downloads"`
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

	// --- 1️⃣ کیا یوزر نے نمبر سلیکٹ کیا ہے؟ ---
	if isNumber(input) {
		index, _ := strconv.Atoi(input)
		
		movieMutex.Lock()
		movies, exists := searchCache[senderJID]
		movieMutex.Unlock()

		if exists && index > 0 && index <= len(movies) {
			selectedMovie := movies[index-1]
			
			react(client, v.Info.Chat, v.Info.ID, "🔄")
			replyMessage(client, v, fmt.Sprintf("🔎 *Checking files for:* %s\nPlease wait...", selectedMovie.Title))
			
			go downloadFromIdentifier(client, v, selectedMovie)
			return
		}
	}

	// --- 2️⃣ کیا یہ ڈائریکٹ لنک ہے؟ ---
	if strings.HasPrefix(input, "http") {
		react(client, v.Info.Chat, v.Info.ID, "🔗")
		replyMessage(client, v, "⏳ *Processing Direct Link...*")
		go downloadFileDirectly(client, v, input, "Unknown_File")
		return
	}

	// --- 3️⃣ یہ سرچ کوئری ہے! ---
	react(client, v.Info.Chat, v.Info.ID, "🔎")
	go performSearch(client, v, input, senderJID)
}

// --- 🔍 Helper: Search Engine ---
func performSearch(client *whatsmeow.Client, v *events.Message, query string, senderJID string) {
	encodedQuery := url.QueryEscape(fmt.Sprintf("title:(%s) AND mediatype:(movies)", query))
	apiURL := fmt.Sprintf("https://archive.org/advancedsearch.php?q=%s&fl[]=identifier&fl[]=title&fl[]=year&fl[]=downloads&sort[]=downloads+desc&output=json&rows=10", encodedQuery)

	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")

	clientHttp := &http.Client{Timeout: 30 * time.Second}
	resp, err := clientHttp.Do(req)
	
	if err != nil {
		replyMessage(client, v, "❌ Network Error: Could not reach Archive API.")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		replyMessage(client, v, fmt.Sprintf("❌ API Error: %d", resp.StatusCode))
		return
	}

	var result IAResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		replyMessage(client, v, "❌ Data Parse Error (Invalid JSON).")
		return
	}

	docs := result.Response.Docs
	if len(docs) == 0 {
		replyMessage(client, v, "🚫 No movies found. Try a different name.")
		return
	}

	var movieList []MovieResult
	msgText := fmt.Sprintf("🎬 *Archive Results for:* '%s'\n\n", query)

	for i, doc := range docs {
		yearStr := fmt.Sprintf("%v", doc.Year)
		
		dlCount := 0
		switch val := doc.Downloads.(type) {
		case float64:
			dlCount = int(val)
		case string:
			dlCount, _ = strconv.Atoi(val)
		}

		movieList = append(movieList, MovieResult{
			Identifier: doc.Identifier,
			Title:      doc.Title,
			Year:       yearStr,
			Downloads:  dlCount,
		})
		msgText += fmt.Sprintf("*%d.* %s (%s)\n", i+1, doc.Title, yearStr)
	}
	
	msgText += "\n👇 *Reply with a number to download.*"

	movieMutex.Lock()
	searchCache[senderJID] = movieList
	movieMutex.Unlock()

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
	fmt.Println("🔍 [ARCHIVE] Fetching metadata for:", movie.Identifier)
	
	metaURL := fmt.Sprintf("https://archive.org/metadata/%s", movie.Identifier)
	req, _ := http.NewRequest("GET", metaURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	
	clientHttp := &http.Client{Timeout: 30 * time.Second}
	resp, err := clientHttp.Do(req)
	
	if err != nil { return }
	defer resp.Body.Close()

	var meta IAMetadata
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		replyMessage(client, v, "❌ Metadata Error: JSON parse failed.")
		return
	}

	bestFile := ""
	maxSize := int64(0)

	for _, f := range meta.Files {
		fName := strings.ToLower(f.Name)
		if strings.HasSuffix(fName, ".mp4") || strings.HasSuffix(fName, ".mkv") {
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
	sizeMB := float64(maxSize) / (1024 * 1024)
	
	// 🔥 Warning if file will be split
	extraWarning := ""
	if sizeMB > 1500 {
		extraWarning = "\n⚠️ *File > 1.5GB:* It will be sent in parts."
	}

	infoMsg := fmt.Sprintf("🚀 *Starting Download!*\n\n🎬 *Title:* %s\n📊 *Size:* %.2f MB%s\n\n_Downloading & Processing..._", movie.Title, sizeMB, extraWarning)
	replyMessage(client, v, infoMsg)
	
	downloadFileDirectly(client, v, finalURL, movie.Title)
}

// --- 🚀 Core Downloader (Auto-Splitter) ---
func downloadFileDirectly(client *whatsmeow.Client, v *events.Message, urlStr string, customTitle string) {
	req, _ := http.NewRequest("GET", urlStr, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	
	clientHttp := &http.Client{Timeout: 0} 
	resp, err := clientHttp.Do(req)
	if err != nil {
		replyMessage(client, v, fmt.Sprintf("❌ Connection Error: %v", err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		replyMessage(client, v, fmt.Sprintf("❌ Server Error: HTTP %d", resp.StatusCode))
		return
	}

	fileName := customTitle
	if fileName == "Unknown_File" {
		parts := strings.Split(urlStr, "/")
		fileName = parts[len(parts)-1]
	}
	fileName = strings.ReplaceAll(fileName, "/", "_")
	if !strings.Contains(fileName, ".") { fileName += ".mp4" }

	// Temp File create
	tempFile := fmt.Sprintf("temp_%d_%s", time.Now().UnixNano(), fileName)
	out, err := os.Create(tempFile)
	if err != nil {
		replyMessage(client, v, "❌ System Error: Could not create temp file.")
		return
	}
	
	// Download to Disk
	_, err = io.Copy(out, resp.Body)
	out.Close()

	if err != nil {
		replyMessage(client, v, "❌ Download Interrupted.")
		os.Remove(tempFile)
		return
	}

	// 📏 Check File Size
	fileInfo, err := os.Stat(tempFile)
	if err != nil {
		os.Remove(tempFile)
		return
	}
	fileSize := fileInfo.Size()
	
	// 🔥 SPLIT LOGIC 🔥
	// 1.5 GB Limit (1500 * 1024 * 1024)
	const MaxSize = 1500 * 1024 * 1024 

	if fileSize > MaxSize {
		// اگر فائل 1.5 GB سے بڑی ہے تو اسپلٹ کریں
		fmt.Printf("⚠️ File Size: %d bytes. Starting Split Process...\n", fileSize)
		splitAndSend(client, v, tempFile, fileName, MaxSize)
	} else {
		// اگر چھوٹی ہے تو ڈائریکٹ بھیج دیں
		sendSingleFile(client, v, tempFile, fileName)
	}
}

// 📤 Helper: Send Single File
func sendSingleFile(client *whatsmeow.Client, v *events.Message, path string, name string) {
	defer os.Remove(path)

	// فائل ریڈ کریں (یہ ریم میں لوڈ ہوگی، 1.5GB تک ریم ہینڈل کر لیتی ہے اگر سرور اچھا ہو)
	// لیکن چونکہ آپ کے پاس 32GB ریم ہے، یہ محفوظ ہے۔
	fileData, err := os.ReadFile(path)
	if err != nil { return }

	fmt.Println("✅ [ARCHIVE] Uploading single file...")
	up, err := client.Upload(context.Background(), fileData, whatsmeow.MediaDocument)
	if err != nil {
		replyMessage(client, v, fmt.Sprintf("❌ Upload Failed: %v", err))
		return
	}

	sendDocMsg(client, v, up, name, "✅ Complete Movie")
}

// 🔪 Helper: Split and Send (Low RAM Usage)
func splitAndSend(client *whatsmeow.Client, v *events.Message, sourcePath string, originalName string, chunkSize int64) {
	defer os.Remove(sourcePath)

	file, err := os.Open(sourcePath)
	if err != nil {
		replyMessage(client, v, "❌ Error opening file for splitting.")
		return
	}
	defer file.Close()

//	buffer := make([]byte, 1024*32) // 32KB buffer for copying
	partNum := 1

	for {
		// پارٹ کا نام بنائیں
		partName := fmt.Sprintf("%s.part%d.mp4", originalName, partNum)
		tempPartPath := fmt.Sprintf("temp_part_%d_%d.mp4", time.Now().UnixNano(), partNum)

		// نیا پارٹ فائل بنائیں
		partFile, err := os.Create(tempPartPath)
		if err != nil {
			replyMessage(client, v, "❌ Error creating part file.")
			return
		}

		// کاپی کریں (صرف 1.5GB تک)
		// io.CopyN ڈیٹا کو سورس سے پارٹ فائل میں کاپی کرے گا بغیر پوری ریم بھرے
		written, err := io.CopyN(partFile, file, chunkSize)
		partFile.Close()

		if written > 0 {
			fmt.Printf("📤 Uploading Part %d (%d bytes)...\n", partNum, written)
			
			// پارٹ کو میموری میں لوڈ کر کے اپلوڈ کریں
			partData, _ := os.ReadFile(tempPartPath)
			up, upErr := client.Upload(context.Background(), partData, whatsmeow.MediaDocument)
			
			// فوری ڈیلیٹ کریں تاکہ ڈسک بھر نہ جائے
			os.Remove(tempPartPath) 

			if upErr != nil {
				replyMessage(client, v, fmt.Sprintf("❌ Failed to upload Part %d", partNum))
				return
			}

			// میسج بھیجیں
			caption := fmt.Sprintf("💿 *Part %d* \n📂 %s", partNum, originalName)
			sendDocMsg(client, v, up, partName, caption)
		}

		// اگر EOF (فائل ختم) ہو گئی تو بریک کریں
		if err == io.EOF {
			break
		}
		if err != nil {
			// اگر کوئی اور ایرر آیا (مطلب ابھی فائل باقی ہے لیکن کاپی نہیں ہوئی)
			break 
		}

		partNum++
	}
	
	react(client, v.Info.Chat, v.Info.ID, "✅")
	replyMessage(client, v, "✅ *All Parts Sent!*")
}

// 📨 Helper: Construct & Send Message
func sendDocMsg(client *whatsmeow.Client, v *events.Message, up whatsmeow.UploadResponse, fileName, caption string) {
	client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		DocumentMessage: &waProto.DocumentMessage{
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			Mimetype:      proto.String("video/mp4"),
			Title:         proto.String(fileName),
			FileName:      proto.String(fileName),
			FileLength:    proto.Uint64(uint64(up.FileLength)), // Correct Size
			FileSHA256:    up.FileSHA256,
			FileEncSHA256: up.FileEncSHA256,
			Caption:       proto.String(caption),
		},
	})
}

func isNumber(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil
}
