package main

import (
	"sync"
	"time"
)

// --- ⚙️ CONFIGURATION ---
const (
	BOT_NAME   = "IMPOSSIBLE BOT V4"
	OWNER_NAME = "Nothing Is Impossible 🜲"
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
}
// ✅ نام کو TikTokState سے بدل کر TTState کر دیا گیا ہے
type TTState struct {
	Title    string
	PlayURL  string
	MusicURL string
	Size     int64
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

type SetupState struct {
	Type    string
	Stage   int
	GroupID string
	User    string
	BotLID  string
}

// --- 🌍 GLOBAL VARIABLES ---
var (
	startTime  = time.Now()
	data       BotData
	dataMutex  sync.RWMutex
	setupMap   = make(map[string]*SetupState)
)