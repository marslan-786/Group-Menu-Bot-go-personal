import sys
import json
import time
import random
from playwright.sync_api import sync_playwright

def search_tiktok(query, limit=10):
    results = []
    with sync_playwright() as p:
        try:
            # 1. Stealth Browser Setup (TikTok ko shak na ho)
            browser = p.chromium.launch(
                headless=True, 
                args=[
                    "--no-sandbox", 
                    "--disable-gpu",
                    "--disable-blink-features=AutomationControlled" # 👈 اہم: یہ بوٹ ڈیٹیکشن روکتا ہے
                ]
            )
            
            context = browser.new_context(
                user_agent="Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
                viewport={"width": 1280, "height": 720},
                device_scale_factor=2,
            )
            
            # Anti-detection scripts injected
            context.add_init_script("Object.defineProperty(navigator, 'webdriver', {get: () => undefined})")
            
            page = context.new_page()
            
            # 2. URL Strategy
            if query.startswith("#"):
                url = f"https://www.tiktok.com/tag/{query[1:]}"
            else:
                url = f"https://www.tiktok.com/search?q={query}"

            # 3. Navigation with Retries
            try:
                page.goto(url, timeout=45000, wait_until="domcontentloaded")
            except:
                pass 

            # 4. Smart Waiting & Scrolling
            # ہم ہارڈ کوڈڈ کلاسز کے بجائے Generic لنکس ڈھونڈیں گے
            for _ in range(4):
                time.sleep(1.5)
                page.keyboard.press("End")

            # 5. Extraction Logic (Universal Selectors)
            data = page.evaluate("""
                () => {
                    const items = [];
                    // TikTok par videos hamesha '/video/' wale links hotay hain
                    const anchors = Array.from(document.querySelectorAll('a[href*="/video/"]'));
                    
                    anchors.forEach(a => {
                        if (items.length >= 15) return;

                        const url = a.href;
                        // Title aksar img ke alt tag mein ya a ke text mein hota hai
                        let title = "";
                        
                        // Koshish 1: Image Alt
                        const img = a.querySelector('img');
                        if (img && img.alt) title = img.alt;
                        
                        // Koshish 2: Inner Text
                        if (!title) title = a.innerText;
                        
                        // Koshish 3: Parent/Sibling Text (Fallback)
                        if (!title && a.parentElement) title = a.parentElement.innerText;

                        // Safai
                        title = title.replace(/\\n/g, ' ').trim();
                        if (title.length > 100) title = title.substring(0, 97) + "...";
                        if (!title) title = "TikTok Trending Video";

                        // Duplicate Check
                        if (url && !items.find(i => i.url === url)) {
                            items.push({ title: title, url: url });
                        }
                    });
                    return items;
                }
            """)
            
            results = data[:limit]

        except Exception as e:
            # Error stderr par bhejen takay Go confuse na ho
            sys.stderr.write(f"Error: {str(e)}\n")
        finally:
            if 'browser' in locals():
                browser.close()
    
    # JSON Output for Go
    print(json.dumps(results))

if __name__ == "__main__":
    query = "funny"
    if len(sys.argv) > 1:
        query = sys.argv[1]
    search_tiktok(query)
