package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"mime/multipart"
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

// ==================== ٹولز سسٹم ====================
func handleToSticker(client *whatsmeow.Client, v *events.Message) {
	// 5 منٹ کا ٹائم آؤٹ (تاکہ بڑی فائل پروسیس ہو سکے)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var quoted *waProto.Message
	if extMsg := v.Message.GetExtendedTextMessage(); extMsg != nil && extMsg.ContextInfo != nil {
		quoted = extMsg.ContextInfo.QuotedMessage
	}

	var media whatsmeow.DownloadableMessage
	isAnimated := false

	if quoted.GetImageMessage() != nil {
		media = quoted.GetImageMessage()
	} else if quoted.GetVideoMessage() != nil {
		media = quoted.GetVideoMessage()
		isAnimated = true
	} else {
		replyMessage(client, v, "❌ Reply to a Photo or Video.")
		return
	}

	react(client, v.Info.Chat, v.Info.ID, "✨")

	data, err := client.Download(ctx, media)
	if err != nil {
		fmt.Println("Download Failed:", err)
		return
	}

	uniqueID := v.Info.ID
	input := fmt.Sprintf("temp_in_%s", uniqueID)
	output := fmt.Sprintf("temp_out_%s.webp", uniqueID)

	os.WriteFile(input, data, 0644)

	var cmd *exec.Cmd

	if isAnimated {
		// --- The "Anti-Retry" Logic ---
		// fps=5: 30 سیکنڈ میں صرف 150 فریمز بنیں گے (یہ واٹس ایپ ہینڈل کر سکتا ہے)
		// compression_level 6: یہ فائل کو نچوڑ کر سب سے چھوٹا کر دے گا تاکہ "Retry" نہ آئے۔
		// q:v 10: کوالٹی لو ہوگی لیکن اسٹیکر پلے ہو جائے گا۔
		cmd = exec.CommandContext(ctx, "ffmpeg", "-y", "-i", input,
			"-vcodec", "libwebp",
			"-filter:v", "fps=5,scale=512:512:force_original_aspect_ratio=increase,crop=512:512",
			"-loop", "0",
			"-preset", "default",
			"-an", "-vsync", "0",
			"-q:v", "10",            // Lowest viable quality
			"-lossless", "0",
			"-compression_level", "6", // Maximum Compression (Takes CPU but makes file safe)
			"-t", "00:01:00",        // 60 Seconds Limit
			output)
	} else {
		cmd = exec.CommandContext(ctx, "ffmpeg", "-y", "-i", input,
			"-vcodec", "libwebp",
			"-filter:v", "scale=512:512:force_original_aspect_ratio=increase,crop=512:512",
			output)
	}

	err = cmd.Run()
	if err != nil {
		fmt.Println("FFmpeg Error:", err)
		replyMessage(client, v, "❌ Video conversion failed.")
		os.Remove(input)
		return
	}

	finalData, _ := os.ReadFile(output)

	// --- Strict Size Limit ---
	// اگر فائل اب بھی 1MB سے اوپر جا رہی ہے تو اسے روک دینا بہتر ہے ورنہ پھر Retry آئے گا۔
	// زیادہ تر Retry تب آتا ہے جب فائل 1MB کے قریب ہو۔
	if len(finalData) > 1500000 { // Limit set to 1.5MB
		replyMessage(client, v, "⚠️ Resulting sticker is still too heavy for WhatsApp.")
		os.Remove(input); os.Remove(output)
		return
	}

	up, err := client.Upload(ctx, finalData, whatsmeow.MediaImage)
	if err != nil {
		fmt.Println("Upload Error:", err)
		replyMessage(client, v, "❌ Upload failed.")
		os.Remove(input); os.Remove(output)
		return
	}

	client.SendMessage(ctx, v.Info.Chat, &waProto.Message{
		StickerMessage: &waProto.StickerMessage{
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			Mimetype:      proto.String("image/webp"),
			FileLength:    proto.Uint64(uint64(len(finalData))),
			FileSHA256:    up.FileSHA256,
			FileEncSHA256: up.FileEncSHA256,
		},
	})

	os.Remove(input)
	os.Remove(output)
}



func handleToImg(client *whatsmeow.Client, v *events.Message) {
	// 🛠️ ریپلائی نکالنے کا ایٹمی طریقہ
	var stickerMsg *waProto.StickerMessage
	if extMsg := v.Message.GetExtendedTextMessage(); extMsg != nil && extMsg.ContextInfo != nil {
		stickerMsg = extMsg.ContextInfo.QuotedMessage.GetStickerMessage()
	}

	if stickerMsg == nil {
		replyMessage(client, v, "❌ *Error:* Please reply to a sticker with *.toimg*")
		return
	}

	react(client, v.Info.Chat, v.Info.ID, "🖼️")
	sendToolCard(client, v, "Media Converter", "WebP to PNG", "⏳ Processing Image...")

	// ڈاؤن لوڈ کریں
	data, err := client.Download(context.Background(), stickerMsg)
	if err != nil { return }

	input := fmt.Sprintf("in_%d.webp", time.Now().UnixNano())
	output := fmt.Sprintf("out_%d.png", time.Now().UnixNano())
	os.WriteFile(input, data, 0644)

	// FFmpeg conversion (Transparency handle کرنے کے لیے)
	exec.Command("ffmpeg", "-y", "-i", input, output).Run()
	
	finalData, _ := os.ReadFile(output)
	up, err := client.Upload(context.Background(), finalData, whatsmeow.MediaImage)
	if err != nil { return }

	client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		ImageMessage: &waProto.ImageMessage{
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			Mimetype:      proto.String("image/png"),
			Caption:       proto.String("✅ *Converted to Image*"),
			FileSHA256:    up.FileSHA256,
			FileEncSHA256: up.FileEncSHA256,
			FileLength:    proto.Uint64(uint64(len(finalData))), // 🛠️ بگ فکس: سائز لازمی ہے
		},
	})
	os.Remove(input); os.Remove(output)
}

func handleToMedia(client *whatsmeow.Client, v *events.Message, isGif bool) {
	var stickerMsg *waProto.StickerMessage
	if extMsg := v.Message.GetExtendedTextMessage(); extMsg != nil && extMsg.ContextInfo != nil {
		stickerMsg = extMsg.ContextInfo.QuotedMessage.GetStickerMessage()
	}

	if stickerMsg == nil || !stickerMsg.GetIsAnimated() {
		replyMessage(client, v, "❌ Please reply to an *Animated* sticker.")
		return
	}

	react(client, v.Info.Chat, v.Info.ID, "🎥")
	
	data, err := client.Download(context.Background(), stickerMsg)
	if err != nil { return }

	// فائلز کے نام
	inputWebP := fmt.Sprintf("in_%d.webp", time.Now().UnixNano())
	tempGif := fmt.Sprintf("temp_%d.gif", time.Now().UnixNano())
	outputMp4 := fmt.Sprintf("out_%d.mp4", time.Now().UnixNano())

	os.WriteFile(inputWebP, data, 0644)

	// 🛠️ STEP 1: ImageMagick کے ذریعے WebP کو GIF میں تبدیل کریں (Animation بچانے کے لیے)
	// -coalesce لیئرز کو مکس ہونے سے روکتا ہے
	cmdConvert := exec.Command("convert", inputWebP, "-coalesce", tempGif)
	if err := cmdConvert.Run(); err != nil {
		fmt.Printf("🔥 ImageMagick Error: %v\n", err)
		replyMessage(client, v, "❌ Failed to parse sticker animation.")
		os.Remove(inputWebP)
		return
	}

	// 🛠️ STEP 2: اب GIF کو FFmpeg کے ذریعے MP4 بنائیں
	cmd := exec.Command("ffmpeg", "-y",
		"-i", tempGif,          // اب ان پٹ GIF ہے
		"-vf", "scale=trunc(iw/2)*2:trunc(ih/2)*2,format=yuv420p", // Even dimensions
		"-c:v", "libx264",
		"-preset", "faster",
		"-crf", "26",
		"-movflags", "+faststart",
		"-pix_fmt", "yuv420p",
		"-t", "10",
		outputMp4)
	
	outLog, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("🔥 Graphics Engine Error: %s\n", string(outLog))
		replyMessage(client, v, "❌ Graphics Engine failed.")
		os.Remove(inputWebP); os.Remove(tempGif)
		return
	}

	finalData, _ := os.ReadFile(outputMp4)
	up, err := client.Upload(context.Background(), finalData, whatsmeow.MediaVideo)
	if err != nil { 
		os.Remove(inputWebP); os.Remove(tempGif); os.Remove(outputMp4)
		return 
	}

	msg := &waProto.Message{
		VideoMessage: &waProto.VideoMessage{
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			Mimetype:      proto.String("video/mp4"),
			Caption:       proto.String("✅ *Converted by Impossible Media Lab*"),
			FileLength:    proto.Uint64(uint64(len(finalData))),
			FileSHA256:    up.FileSHA256,
			FileEncSHA256: up.FileEncSHA256,
		},
	}

	if isGif {
		msg.VideoMessage.GifPlayback = proto.Bool(true)
	}

	client.SendMessage(context.Background(), v.Info.Chat, msg)
	
	// سب ڈیلیٹ کریں
	os.Remove(inputWebP)
	os.Remove(tempGif)
	os.Remove(outputMp4)
	
	react(client, v.Info.Chat, v.Info.ID, "✅")
}


func handleToURL(client *whatsmeow.Client, v *events.Message) {
	react(client, v.Info.Chat, v.Info.ID, "🔗")
	
	msg := `╔══════════════════╗
║  🔗 UPLOADING MEDIA       
╠══════════════════╣
║ ⏳ Uploading to server... 
║         Please wait...           
╚══════════════════╝`
	replyMessage(client, v, msg)

	d, err := downloadMedia(client, v.Message)
	if err != nil {
		errMsg := `╔═════════════════╗
║  ❌ NO MEDIA FOUND       
╠═══════════════════╣
║ Reply to media to get URL
╚═══════════════════╝`
		replyMessage(client, v, errMsg)
		return
	}

	uploadURL := uploadToCatbox(d)
	
	resultMsg := fmt.Sprintf(`╔═════════════════╗
║  🔗 MEDIA UPLOADED        
╠═════════════════╣
║                           
║  📎 *Direct Link:*        
║  %s                       
║                           
║ ✅ *Successfully Uploaded*
║                           
╚═══════════════════╝`, uploadURL)

	replyMessage(client, v, resultMsg)
}

func handleTranslate(client *whatsmeow.Client, v *events.Message, args []string) {
	react(client, v.Info.Chat, v.Info.ID, "🌍")

	t := strings.Join(args, " ")
	if t == "" {
		if v.Message.ExtendedTextMessage != nil {
			q := v.Message.ExtendedTextMessage.GetContextInfo().GetQuotedMessage()
			if q != nil {
				t = q.GetConversation()
			}
		}
	}

	if t == "" {
		msg := `╔══════════════╗
║   🌍 TRANSLATOR            
╠══════════════╣
║                           
║  Usage:                   
║  .tr <text>               
║                           
║  Or reply to message with:
║  .tr                      
║                           
╚═══════════════════╝`
		replyMessage(client, v, msg)
		return
	}

	r, _ := http.Get(fmt.Sprintf("https://translate.googleapis.com/translate_a/single?client=gtx&sl=auto&tl=ur&dt=t&q=%s", url.QueryEscape(t)))
	var res []interface{}
	json.NewDecoder(r.Body).Decode(&res)

	if len(res) > 0 {
		translated := res[0].([]interface{})[0].([]interface{})[0].(string)
		msg := fmt.Sprintf(`╔═══════════════════╗
║ 🌍 TRANSLATION RESULT    
╠═══════════════════╣
║                           
║  📝 *Original:*           
║  %s                       
║                           
║  📝 *Translated:*         
║  %s                       
║                           
╚════════════════════╝`, t, translated)

		replyMessage(client, v, msg)
	} else {
		errMsg := `╔══════════════════╗
║ ❌ TRANSLATION FAILED    
╠══════════════════╣
║  Could not translate text 
║  Please try again         
╚══════════════════╝`
		replyMessage(client, v, errMsg)
	}
}

func handleVV(client *whatsmeow.Client, v *events.Message) {
	react(client, v.Info.Chat, v.Info.ID, "🫣")
	fmt.Printf("\n--- [VV FINAL DEBUG START] ---\n")

	// 1. Get Context Info
	cInfo := v.Message.GetExtendedTextMessage().GetContextInfo()
	if cInfo == nil {
		fmt.Println("❌ [VV] No ContextInfo found")
		replyMessage(client, v, "⚠️ Please reply to a media message.")
		return
	}

	quoted := cInfo.GetQuotedMessage()
	if quoted == nil {
		fmt.Println("❌ [VV] Quoted message is nil")
		return
	}

	// 2. Advanced Media Extraction (Robust Logic)
	var (
		imgMsg *waProto.ImageMessage
		vidMsg *waProto.VideoMessage
		audMsg *waProto.AudioMessage
	)

	// Direct check
	if quoted.ImageMessage != nil {
		imgMsg = quoted.ImageMessage
	} else if quoted.VideoMessage != nil {
		vidMsg = quoted.VideoMessage
	} else if quoted.AudioMessage != nil {
		audMsg = quoted.AudioMessage
	} else {
		// Nested ViewOnce check (V1 & V2)
		vo := quoted.GetViewOnceMessage().GetMessage()
		if vo == nil {
			vo = quoted.GetViewOnceMessageV2().GetMessage()
		}
		if vo != nil {
			if vo.ImageMessage != nil { imgMsg = vo.ImageMessage }
			if vo.VideoMessage != nil { vidMsg = vo.VideoMessage }
		}
	}

	// 3. Validation Check
	if imgMsg == nil && vidMsg == nil && audMsg == nil {
		fmt.Println("❌ [VV] No supported media found in extraction.")
		replyMessage(client, v, "❌ No image/video/audio found to copy.")
		return
	}

	// 4. Download and Upload
	ctx := context.Background()
	var (
		data []byte
		err  error
		mType whatsmeow.MediaType
	)

	if imgMsg != nil {
		fmt.Println("📸 [VV] Downloading Image...")
		data, err = client.Download(ctx, imgMsg)
		mType = whatsmeow.MediaImage
	} else if vidMsg != nil {
		fmt.Println("🎥 [VV] Downloading Video...")
		data, err = client.Download(ctx, vidMsg)
		mType = whatsmeow.MediaVideo
	} else if audMsg != nil {
		fmt.Println("🎤 [VV] Downloading Audio...")
		data, err = client.Download(ctx, audMsg)
		mType = whatsmeow.MediaAudio
	}

	if err != nil || len(data) == 0 {
		fmt.Printf("❌ [VV] Download Failed: %v (Size: %d)\n", err, len(data))
		return
	}

	up, err := client.Upload(ctx, data, mType)
	if err != nil {
		fmt.Printf("❌ [VV] Upload Failed: %v\n", err)
		return
	}

	// 5. Build Perfect Protobuf (Including FileLength)
	var finalMsg waProto.Message
	caption := "📂 *RETRIEVED MEDIA*\n\n✅ Successfully copied."

	if imgMsg != nil {
		finalMsg.ImageMessage = &waProto.ImageMessage{
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			Mimetype:      proto.String("image/jpeg"),
			FileSHA256:    up.FileSHA256,
			FileEncSHA256: up.FileEncSHA256,
			FileLength:    proto.Uint64(uint64(len(data))), // ✅ لازمی فیلڈ
			Caption:       proto.String(caption),
		}
	} else if vidMsg != nil {
		finalMsg.VideoMessage = &waProto.VideoMessage{
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			Mimetype:      proto.String("video/mp4"),
			FileSHA256:    up.FileSHA256,
			FileEncSHA256: up.FileEncSHA256,
			FileLength:    proto.Uint64(uint64(len(data))), // ✅ لازمی فیلڈ
			Caption:       proto.String(caption),
		}
	} else if audMsg != nil {
		finalMsg.AudioMessage = &waProto.AudioMessage{
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			Mimetype:      proto.String("audio/ogg; codecs=opus"),
			FileSHA256:    up.FileSHA256,
			FileEncSHA256: up.FileEncSHA256,
			FileLength:    proto.Uint64(uint64(len(data))), // ✅ لازمی فیلڈ
			PTT:           proto.Bool(false), // Baileys کی طرح نارمل آڈیو
		}
	}

	// 6. Final Clean Send
	resp, sendErr := client.SendMessage(ctx, v.Info.Chat, &finalMsg)
	if sendErr != nil {
		fmt.Printf("❌ [VV] Final Send Error: %v\n", sendErr)
	} else {
		fmt.Printf("🚀 [VV] DONE! Message Sent. ID: %s\n", resp.ID)
	}
	fmt.Printf("--- [VV FINAL DEBUG END] ---\n")
}




// ==================== میڈیا ہیلپرز ====================
func downloadMedia(client *whatsmeow.Client, m *waProto.Message) ([]byte, error) {
	var d whatsmeow.DownloadableMessage
	if m.ImageMessage != nil {
		d = m.ImageMessage
	} else if m.VideoMessage != nil {
		d = m.VideoMessage
	} else if m.DocumentMessage != nil {
		d = m.DocumentMessage
	} else if m.StickerMessage != nil {
		d = m.StickerMessage
	} else if m.ExtendedTextMessage != nil && m.ExtendedTextMessage.ContextInfo != nil {
		q := m.ExtendedTextMessage.ContextInfo.QuotedMessage
		if q != nil {
			if q.ImageMessage != nil {
				d = q.ImageMessage
			} else if q.VideoMessage != nil {
				d = q.VideoMessage
			} else if q.StickerMessage != nil {
				d = q.StickerMessage
			}
		}
	}
	if d == nil {
		return nil, fmt.Errorf("no media")
	}
	return client.Download(context.Background(), d)
}

func uploadToCatbox(d []byte) string {
	b := new(bytes.Buffer)
	w := multipart.NewWriter(b)
	p, _ := w.CreateFormFile("fileToUpload", "f.jpg")
	p.Write(d)
	w.WriteField("reqtype", "fileupload")
	w.Close()
	r, _ := http.Post("https://catbox.moe/user/api.php", w.FormDataContentType(), b)
	res, _ := ioutil.ReadAll(r.Body)
	return string(res)
}