import sys
import json
import time
from playwright.sync_api import sync_playwright

def search_tiktok(query, limit=10):
    results = []
    with sync_playwright() as p:
        try:
            browser = p.chromium.launch(headless=True, args=["--no-sandbox", "--disable-gpu"])
            context = browser.new_context(
                user_agent="Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
            )
            page = context.new_page()
            
            # سرچ یو آر ایل
            # اگر ہیش ٹیگ ہے تو ٹیگ پیج، ورنہ سرچ پیج
            if query.startswith("#"):
                url = f"https://www.tiktok.com/tag/{query[1:]}"
            else:
                url = f"https://www.tiktok.com/search?q={query}"

            page.goto(url, timeout=60000)
            
            # تھوڑا انتظار اور اسکرول تاکہ ویڈیوز لوڈ ہوں
            try:
                page.wait_for_selector('a[href*="/video/"]', timeout=10000)
            except:
                pass # اگر فوراً نہ ملے تو خیر ہے، اسکرول کریں گے

            for _ in range(3):
                page.keyboard.press("End")
                time.sleep(2)

            # ڈیٹا نکالیں (Title اور Link)
            data = page.evaluate("""
                () => {
                    const items = [];
                    // TikTok کی مختلف کلاسز ہو سکتی ہیں، ہم جنرک طریقہ استعمال کریں گے
                    const links = Array.from(document.querySelectorAll('a[href*="/video/"]'));
                    
                    links.forEach(a => {
                        if (items.length >= 15) return; // تھوڑے زیادہ اٹھائیں تاکہ فلٹر کر سکیں
                        
                        const url = a.href;
                        // اکثر ٹائٹل امیج کے alt میں یا قریبی div میں ہوتا ہے
                        let title = a.innerText || a.getAttribute('title') || "TikTok Video";
                        
                        // کلین اپ
                        if (url && !items.find(i => i.url === url)) {
                            items.push({ title: title.replace(/\\n/g, ' ').trim(), url: url });
                        }
                    });
                    return items;
                }
            """)
            
            results = data[:limit] # جتنے چاہیے اتنے رکھیں

        except Exception as e:
            # ایرر کو اگنور کریں اور جتنے رزلٹ ملے وہ بھیج دیں
            pass
        finally:
            browser.close()
    
    # JSON پرنٹ کریں (یہ Go پڑھے گا)
    print(json.dumps(results))

if __name__ == "__main__":
    if len(sys.argv) > 1:
        search_tiktok(sys.argv[1])
    else:
        print("[]")
