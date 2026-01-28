package main

import (
	"sync"
	"time"
)

// --- ⚙️ CONFIGURATION ---
const (
	BOT_NAME   = "𝙎𝙞𝙡𝙚𝙣𝙩 𝙃𝙖𝙘𝙠𝙚𝙧𝙨 V4"
	OWNER_NAME = "𝙎𝙞𝙡𝙚𝙣𝙩 𝙃𝙖𝙘𝙠𝙚𝙧𝙨 🜲"
)

// --- 💾 DATA STRUCTURES ---
type GroupSettings struct {
	ChatID         string         `bson:"chat_id" json:"chat_id"`
	Mode           string         `bson:"mode" json:"mode"`
	Antilink       bool           `bson:"antilink" json:"antilink"`
	AntilinkAdmin  bool           `bson:"antilink_admin" json:"antilink_admin"`
	AntilinkAction string         `bson:"antilink_action" json:"antilink_action"`
	AntiPic        bool           `bson:"antipic" json:"antipic"`
	AntiVideo      bool           `bson:"antivideo" json:"antivideo"`
	AntiSticker    bool           `bson:"antisticker" json:"antisticker"`
	Warnings       map[string]int `bson:"warnings" json:"warnings"`
	Welcome        bool   `json:"welcome"`
}
// ✅ نام کو TikTokState سے بدل کر TTState کر دیا گیا ہے
type TTState struct {
	Title    string
	PlayURL  string
	MusicURL string
	Size     int64
}
// یہ یوٹیوب سرچ کا سیشن سنبھالے گا
type YTSession struct {
	Results  []YTSResult
	SenderID string
	BotLID   string
}

// یہ ڈاؤنلوڈ مینیو (MP3/MP4) کا اسٹیٹ سنبھالے گا
type YTState struct {
	Url      string
	Title    string
	SenderID string
	BotLID   string // ✅ یہ فیلڈ ایڈ کر دی
}

// اگر YTSResult پہلے سے نہیں ہے تو اسے بھی ڈال دیں
type YTSResult struct {
	Title string
	Url   string
}

type BotData struct {
	ID            string   `bson:"_id" json:"id"`
	Prefix        string   `bson:"prefix" json:"prefix"`
	AlwaysOnline  bool     `bson:"always_online" json:"always_online"`
	AutoRead      bool     `bson:"auto_read" json:"auto_read"`
	AutoReact     bool     `bson:"auto_react" json:"auto_react"`
	AutoStatus    bool     `bson:"auto_status" json:"auto_status"`
	StatusReact   bool     `bson:"status_react" json:"status_react"`
	StatusTargets []string `bson:"status_targets" json:"status_targets"`
}

// SetupState بوٹ کے سیکیورٹی سیٹ اپ کے سیشن کو سنبھالتا ہے
type SetupState struct {
	Type     string // اینٹی لنک، اینٹی پک، وغیرہ (Feature Name)
	Stage    int    // پہلا اسٹیج ہے یا دوسرا (Current Step)
	GroupID  string // کس گروپ میں سیٹ اپ ہو رہا ہے
	User     string // کون سا ایڈمن سیٹ اپ کر رہا ہے
	BotLID   string // کس بوٹ کے ذریعے سیٹ اپ ہو رہا ہے (Multi-Bot Fix)
	BotMsgID string // بوٹ کے بھیجے گئے کارڈ کی یونیک آئی ڈی (Reply Check)
}

// --- 🌍 GLOBAL VARIABLES ---
var (
	startTime  = time.Now()
	data       BotData
	dataMutex  sync.RWMutex
	setupMap   = make(map[string]*SetupState)
)