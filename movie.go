package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types/events"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"google.golang.org/protobuf/proto"
)

// --- ⚙️ CONFIGURATION ---
const ChunkSize int64 = 1024 * 1024 * 1024 // 1GB Limit

// --- 🧠 MEMORY SYSTEM ---
type ArchiveResult struct {
	Identifier string
	Title      string
	Year       string
	Downloads  int
	Type       string // New: To verify file type
}

var archiveCache = make(map[string][]ArchiveResult)
var archiveMutex sync.Mutex

// API Response Structures
type IAHeader struct {
	Identifier string      `json:"identifier"`
	Title      string      `json:"title"`
	Year       interface{} `json:"year"`
	Downloads  interface{} `json:"downloads"`
	Mediatype  string      `json:"mediatype"`
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

// --- 🎮 MAIN HANDLER (Updated) ---
// mode can be "movie" or "universal"
func handleArchive(client *whatsmeow.Client, v *events.Message, input string, mode string) {
	if input == "" { return }
	input = strings.TrimSpace(input)
	senderJID := v.Info.Sender.String()

	// 1️⃣ Number Selection (Download Logic)
	if isNumber(input) {
		index, _ := strconv.Atoi(input)
		
		archiveMutex.Lock()
		results, exists := archiveCache[senderJID]
		archiveMutex.Unlock()

		if exists && index > 0 && index <= len(results) {
			selected := results[index-1]
			
			react(client, v.Info.Chat, v.Info.ID, "🔄")
			replyMessage(client, v, fmt.Sprintf("🔎 *Checking files for:* %s\nType: %s", selected.Title, selected.Type))
			
			// اگر کتاب ہے تو PDF ڈھونڈے گا، مووی ہے تو Video
			go downloadFromArchive(client, v, selected)
			return
		}
	}

	// 2️⃣ Direct Link
	if strings.HasPrefix(input, "http") {
		react(client, v.Info.Chat, v.Info.ID, "🔗")
		go downloadFileDirectly(client, v, input, "Unknown_File")
		return
	}

	// 3️⃣ Search Query
	react(client, v.Info.Chat, v.Info.ID, "🔎")
	go performArchiveSearch(client, v, input, senderJID, mode)
}

// --- 🔍 Helper: Search Engine (Updated for Modes) ---
func performArchiveSearch(client *whatsmeow.Client, v *events.Message, query string, senderJID string, mode string) {
	// 🔥 Dynamic Query Builder
	searchQ := fmt.Sprintf("title:(%s)", query)
	
	// اگر مووی موڈ ہے تو صرف موویز، ورنہ سب کچھ
	if mode == "movie" {
		searchQ += " AND mediatype:(movies)"
	}
	
	encodedQuery := url.QueryEscape(searchQ)
	// fields میں mediatype بھی منگوا لیا
	apiURL := fmt.Sprintf("https://archive.org/advancedsearch.php?q=%s&fl[]=identifier&fl[]=title&fl[]=year&fl[]=downloads&fl[]=mediatype&sort[]=downloads+desc&output=json&rows=10", encodedQuery)

	req, _ := http.NewRequest("GET", apiURL, nil)
	clientHttp := &http.Client{Timeout: 30 * time.Second}
	resp, err := clientHttp.Do(req)
	
	if err != nil || resp.StatusCode != 200 {
		replyMessage(client, v, "❌ Archive API Error.")
		return
	}
	defer resp.Body.Close()

	var result IAResponse
	json.NewDecoder(resp.Body).Decode(&result)

	docs := result.Response.Docs
	if len(docs) == 0 {
		replyMessage(client, v, "🚫 No results found on Archive.org")
		return
	}

	var list []ArchiveResult
	// 🏷️ Header based on Mode
	titlePrefix := "🏛️ *Archive Universal Results*"
	if mode == "movie" { titlePrefix = "🎬 *Movie Search Results*" }
	
	msgText := fmt.Sprintf("%s for: '%s'\n\n", titlePrefix, query)

	for i, doc := range docs {
		yearStr := fmt.Sprintf("%v", doc.Year)
		dlCount := 0
		switch val := doc.Downloads.(type) {
		case float64: dlCount = int(val)
		case string: dlCount, _ = strconv.Atoi(val)
		}

		// List میں شامل کریں
		list = append(list, ArchiveResult{
			Identifier: doc.Identifier,
			Title:      doc.Title,
			Year:       yearStr,
			Downloads:  dlCount,
			Type:       doc.Mediatype, // e.g., texts, movies, audio
		})
		
		// Icon based on type
		icon := "📁"
		if doc.Mediatype == "movies" { icon = "🎬" }
		if doc.Mediatype == "texts" { icon = "📚" }
		if doc.Mediatype == "audio" { icon = "🎵" }

		msgText += fmt.Sprintf("*%d.* %s %s (%s)\n", i+1, icon, doc.Title, yearStr)
	}
	
	msgText += "\n👇 *Reply with a number to download.*"

	archiveMutex.Lock()
	archiveCache[senderJID] = list
	archiveMutex.Unlock()

	// Send Menu
	client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{
			Text: proto.String(msgText),
			ContextInfo: &waProto.ContextInfo{StanzaID: proto.String(v.Info.ID), Participant: proto.String(senderJID), QuotedMessage: v.Message},
		},
	})
}

// --- 📥 Helper: Smart File Picker (PDF vs Video) ---
func downloadFromArchive(client *whatsmeow.Client, v *events.Message, item ArchiveResult) {
	metaURL := fmt.Sprintf("https://archive.org/metadata/%s", item.Identifier)
	resp, err := http.Get(metaURL)
	if err != nil { return }
	defer resp.Body.Close()

	var meta IAMetadata
	json.NewDecoder(resp.Body).Decode(&meta)

	bestFile := ""
	maxSize := int64(0)
	
	// 🔥 Intelligent Filter
	targetExts := []string{}
	
	if item.Type == "texts" {
		// اگر کتاب ہے تو PDF یا EPUB ڈھونڈو
		targetExts = []string{".pdf", ".epub"}
	} else if item.Type == "movies" {
		// اگر مووی ہے تو Video
		targetExts = []string{".mp4", ".mkv", ".avi"}
	} else if item.Type == "audio" {
		targetExts = []string{".mp3", ".flac"}
	} else {
		// اگر کچھ اور ہے (جیسے سافٹ ویئر) تو ZIP/ISO/EXE
		targetExts = []string{".zip", ".iso", ".rar", ".pdf", ".mp4"}
	}

	for _, f := range meta.Files {
		fName := strings.ToLower(f.Name)
		s, _ := strconv.ParseInt(f.Size, 10, 64)

		// Check extensions
		for _, ext := range targetExts {
			if strings.HasSuffix(fName, ext) {
				// سب سے بڑی فائل اٹھائے (High Quality)
				if s > maxSize {
					maxSize = s
					bestFile = f.Name
				}
			}
		}
	}

	if bestFile == "" {
		replyMessage(client, v, "❌ No suitable file found in this archive item.")
		return
	}

	finalURL := fmt.Sprintf("https://archive.org/download/%s/%s", item.Identifier, url.PathEscape(bestFile))
	sizeMB := float64(maxSize) / (1024 * 1024)

	replyMessage(client, v, fmt.Sprintf("🚀 *Downloading:* %s\n📂 *Type:* %s\n📦 *Size:* %.2f MB", item.Title, item.Type, sizeMB))
	
	downloadFileDirectly(client, v, finalURL, item.Title)
}

// --- 🚀 Core Downloader (Same as before) ---

// --- 🚀 Core Downloader (Optimized Disk Stream) ---
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

	// Name Cleaning
	fileName := customTitle
	if fileName == "Unknown_File" {
		parts := strings.Split(urlStr, "/")
		fileName = parts[len(parts)-1]
	}
	fileName = strings.ReplaceAll(fileName, "/", "_")
	if !strings.Contains(fileName, ".") { fileName += ".mp4" }

	partNum := 1
	copyBuffer := make([]byte, 32*1024) 

	// 🔄 Stream Loop: Download 1GB -> Send -> Delete
	for {
		tempPartPath := fmt.Sprintf("stream_cache_%d_part_%d.mp4", time.Now().UnixNano(), partNum)
		
		// 1. Create File on Disk
		partFile, err := os.Create(tempPartPath)
		if err != nil {
			replyMessage(client, v, "All Parts Sent Successfully ✅")
			return
		}

		// 2. Stream Data (Corrected io.CopyBuffer args)
		// ✅ Fix: io.CopyBuffer(DST, SRC, BUFFER)
		written, err := io.CopyBuffer(partFile, io.LimitReader(resp.Body, ChunkSize), copyBuffer)
		partFile.Close() 

		if written > 0 {
			fmt.Printf("💾 Part %d Saved (%.2f MB). Uploading...\n", partNum, float64(written)/(1024*1024))
			
			// 3. Upload
			partData, _ := os.ReadFile(tempPartPath)
			up, upErr := client.Upload(context.Background(), partData, whatsmeow.MediaDocument)
			
			// 4. Cleanup
			partData = nil
			runtime.GC()
			debug.FreeOSMemory()
			os.Remove(tempPartPath) 

			if upErr != nil {
				replyMessage(client, v, fmt.Sprintf("❌ Failed to upload Part %d", partNum))
				return
			}

			// 5. Send Message
			caption := fmt.Sprintf("💿 *Part %d* \n📂 %s", partNum, fileName)
			if partNum == 1 && err == io.EOF {
				caption = fmt.Sprintf("✅ *Complete Movie* \n📂 %s", fileName)
			}
			
			partName := fmt.Sprintf("%s_Part_%d.mp4", fileName, partNum)
			sendDocMsg(client, v, up, partName, caption)
		}

		if err == io.EOF { break }
		if err != nil {
			replyMessage(client, v, "❌ Stream Interrupted.")
			break
		}

		partNum++
	}
	
	react(client, v.Info.Chat, v.Info.ID, "✅")
}

// ♻️ Restored Helper: splitAndSend 
// (یہ فنکشن اس فائل میں یوز نہیں ہو رہا لیکن downloader.go کو اس کی ضرورت ہے، اس لیے واپس ڈالا ہے)
func splitAndSend(client *whatsmeow.Client, v *events.Message, sourcePath string, originalName string, chunkSize int64) {
	defer os.Remove(sourcePath)

	file, err := os.Open(sourcePath)
	if err != nil { return }
	defer file.Close()

	partNum := 1
	for {
		partName := fmt.Sprintf("%s.part%d.mp4", originalName, partNum)
		tempPartPath := fmt.Sprintf("temp_part_%d_%d.mp4", time.Now().UnixNano(), partNum)

		partFile, err := os.Create(tempPartPath)
		if err != nil { return }

		written, err := io.CopyN(partFile, file, chunkSize)
		partFile.Close()

		if written > 0 {
			partData, _ := os.ReadFile(tempPartPath)
			up, upErr := client.Upload(context.Background(), partData, whatsmeow.MediaDocument)
			os.Remove(tempPartPath) 

			if upErr == nil {
				caption := fmt.Sprintf("💿 *Part %d* \n📂 %s", partNum, originalName)
				sendDocMsg(client, v, up, partName, caption)
			}
		}

		if err == io.EOF { break }
		if err != nil { break }
		partNum++
	}
}

// 📨 Helper: Send Message
func sendDocMsg(client *whatsmeow.Client, v *events.Message, up whatsmeow.UploadResponse, fileName, caption string) {
	client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		DocumentMessage: &waProto.DocumentMessage{
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			Mimetype:      proto.String("video/mp4"),
			Title:         proto.String(fileName),
			FileName:      proto.String(fileName),
			FileLength:    proto.Uint64(uint64(up.FileLength)),
			FileSHA256:    up.FileSHA256,
			FileEncSHA256: up.FileEncSHA256,
			Caption:       proto.String(caption),
		},
	})
}

// --- Shared Helpers (Assuming these are needed locally if not in utils) ---
func isNumber(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil
}