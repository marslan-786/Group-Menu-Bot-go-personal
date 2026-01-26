package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
    "io"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types/events"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"google.golang.org/protobuf/proto"
)

// --- 📚 LIBGEN SYSTEM ---
type BookResult struct {
	ID        string
	Title     string
	Author    string
	Extension string
	Size      string
	MirrorURL string
}

var bookCache = make(map[string][]BookResult)
var bookMutex sync.Mutex

// --- HANDLER ---
func handleLibgen(client *whatsmeow.Client, v *events.Message, input string) {
	if input == "" { return }
	senderJID := v.Info.Sender.String()

	// 1️⃣ اگر نمبر ہے تو ڈاؤن لوڈ
	if isNumber(input) {
		index, _ := strconv.Atoi(input)
		bookMutex.Lock()
		books, exists := bookCache[senderJID]
		bookMutex.Unlock()

		if exists && index > 0 && index <= len(books) {
			book := books[index-1]
			react(client, v.Info.Chat, v.Info.ID, "📖")
			replyMessage(client, v, fmt.Sprintf("⏳ *Fetching PDF Link for:* %s\nPlease wait...", book.Title))
			go fetchAndDownloadBook(client, v, book)
			return
		}
	}

	// 2️⃣ ورنہ سرچ
	react(client, v.Info.Chat, v.Info.ID, "📚")
	go searchLibgen(client, v, input, senderJID)
}

// --- 🕵️ SCRAPER ---
func searchLibgen(client *whatsmeow.Client, v *events.Message, query string, senderJID string) {
	// Libgen Mirror (stable)
	baseURL := "https://libgen.is/search.php"
	u, _ := url.Parse(baseURL)
	q := u.Query()
	q.Set("req", query)
	q.Set("res", "10")
	q.Set("column", "def")
	u.RawQuery = q.Encode()

	resp, err := http.Get(u.String())
	if err != nil {
		replyMessage(client, v, "❌ Libgen Server Unreachable.")
		return
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	html := string(bodyBytes)

	// 🔥 Regex Parsing (HTML Table Scraper)
	// یہ Regex ٹیبل کی قطاروں سے ڈیٹا نکالے گا
	// Row Pattern: <tr>...ID...Author...Title...Size...Ext...Mirror...</tr>
	re := regexp.MustCompile(`<tr valign="top">.*?<td>(\d+)</td>.*?<td>(.*?)</td>.*?<a href="(.*?)".*?>(.*?)<.*?<td>(.*?)</td>.*?<td>(.*?)</td>.*?href="(http://library.lol/main/.*?)".*?</tr>`)
	
	matches := re.FindAllStringSubmatch(html, 10) // Top 10 results

	if len(matches) == 0 {
		replyMessage(client, v, "🚫 No books found on Libgen.")
		return
	}

	var results []BookResult
	msgText := fmt.Sprintf("📚 *Libgen Books for:* '%s'\n\n", query)

	for i, m := range matches {
		// m[1]=ID, m[2]=Author, m[4]=Title, m[5]=Publisher, m[6]=Year, m[7]=Pages, m[9]=Size, m[10]=Ext, m[11]=Mirror
		// Regex groups might vary slightly based on HTML structure, this is a simplified robust match
		
		// Clean Title (remove HTML tags)
		title := stripTags(m[4])
		author := stripTags(m[2])
		size := m[6] // Adjust index based on real libgen table column count if needed
        // Note: Regex above is simplified. Let's use specific capturing for Mirror which is key.
        mirror := m[7] // The library.lol link
        
		results = append(results, BookResult{
			Title:     title,
			Author:    author,
			MirrorURL: mirror,
			Extension: "pdf", // Default assumption, actual file check happens later
		})

		msgText += fmt.Sprintf("*%d.* %s\n👤 _%s_ | 📦 %s\n\n", i+1, title, author, "PDF/EPUB")
	}

	msgText += "👇 *Reply with a number to download.*"

	bookMutex.Lock()
	bookCache[senderJID] = results
	bookMutex.Unlock()

	client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{
			Text: proto.String(msgText),
			ContextInfo: &waProto.ContextInfo{StanzaID: proto.String(v.Info.ID), Participant: proto.String(senderJID), QuotedMessage: v.Message},
		},
	})
}

// --- 📥 DOWNLOADER ---
func fetchAndDownloadBook(client *whatsmeow.Client, v *events.Message, book BookResult) {
	// 1. Go to Mirror Page (library.lol)
	resp, err := http.Get(book.MirrorURL)
	if err != nil {
		replyMessage(client, v, "❌ Mirror Link Failed.")
		return
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	html := string(bodyBytes)

	// 2. Find the "GET" or "Cloudflare" link
	// Pattern: <a href="...">GET</a> or <h2><a href="...">GET</a></h2>
	re := regexp.MustCompile(`<h2><a href="(.*?)">GET</a></h2>`)
	match := re.FindStringSubmatch(html)

	if len(match) < 2 {
		replyMessage(client, v, "❌ Could not extract direct download link.")
		return
	}

	directLink := match[1]
	
	// 3. Download using General Downloader logic
	replyMessage(client, v, fmt.Sprintf("🚀 *Downloading Book...*\n%s", book.Title))
	downloadFileDirectly(client, v, directLink, book.Title+".pdf") // Force PDF naming for now
}

func stripTags(content string) string {
	re := regexp.MustCompile(`<.*?>`)
	return re.ReplaceAllString(content, "")
}
