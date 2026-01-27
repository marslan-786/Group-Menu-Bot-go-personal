import sys
import json
import time
import random
import os
from playwright.sync_api import sync_playwright

def print_debug(msg):
    # یہ فنکشن stderr پر لکھے گا تاکہ Go کے لاگز میں نظر آئے
    sys.stderr.write(f"🐍 [PYTHON DEBUG] {msg}\n")
    sys.stderr.flush()

def search_tiktok(query, limit=10):
    results = []
    print_debug(f"🚀 Starting Search for: {query}")

    with sync_playwright() as p:
        try:
            # 1. Browser Launch (Stealth Arguments)
            print_debug("Launching Browser...")
            browser = p.chromium.launch(
                headless=True, 
                args=[
                    "--no-sandbox",
                    "--disable-gpu",
                    "--disable-blink-features=AutomationControlled",
                    "--window-size=1920,1080",
                    "--user-agent=Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36"
                ]
            )
            
            context = browser.new_context(
                viewport={"width": 1920, "height": 1080},
                locale="en-US",
                timezone_id="Asia/Karachi"
            )
            
            # 2. Add Init Script to hide webdriver property
            context.add_init_script("""
                Object.defineProperty(navigator, 'webdriver', {
                    get: () => undefined
                });
            """)

            page = context.new_page()
            
            # 🖥️ Browser Console Logs کو ٹرمینل میں لائیں
            page.on("console", lambda msg: print_debug(f"BROWSER_LOG: {msg.text}"))

            # 3. URL Selection
            if query.startswith("#"):
                url = f"https://www.tiktok.com/tag/{query[1:]}"
            else:
                url = f"https://www.tiktok.com/search?q={query}"

            print_debug(f"Navigating to: {url}")
            
            # 4. Goto Page
            response = page.goto(url, timeout=60000, wait_until="domcontentloaded")
            print_debug(f"Page Load Status: {response.status}")
            
            # 5. Check Page Title (تاکہ پتہ چلے کیپچا تو نہیں)
            page_title = page.title()
            print_debug(f"PAGE TITLE: {page_title}")

            # 6. Scroll Logic
            print_debug("Scrolling down...")
            for i in range(4):
                page.keyboard.press("End")
                time.sleep(2)
            
            # 7. Take Screenshot (Debug ke liye file save hogi)
            # screenshot_path = "tiktok_debug.png"
            # page.screenshot(path=screenshot_path)
            # print_debug(f"📸 Screenshot saved to {screenshot_path}")

            # 8. Extract Data
            print_debug("Extracting links via JS...")
            data = page.evaluate("""
                () => {
                    const items = [];
                    // ہر قسم کا لنک جو ویڈیو کی طرف جا رہا ہو
                    const anchors = Array.from(document.querySelectorAll('a'));
                    
                    anchors.forEach(a => {
                        const href = a.href;
                        if (href && href.includes('/video/') && !items.find(i => i.url === href)) {
                            
                            let title = a.getAttribute('title') || a.innerText;
                            // اگر ٹائٹل نہ ملے تو امیج کا Alt چیک کرو
                            const img = a.querySelector('img');
                            if (!title && img) title = img.alt;
                            
                            // صفائی
                            title = title ? title.replace(/\\n/g, ' ').trim() : "TikTok Viral Video";
                            
                            items.push({ title: title, url: href });
                        }
                    });
                    return items;
                }
            """)
            
            print_debug(f"Found {len(data)} raw items.")
            results = data[:limit]

            # 🔥 9. DUMP HTML IF NO RESULTS (User Demand: Kacha Chittha)
            if len(results) == 0:
                print_debug("❌ NO RESULTS FOUND! Dumping HTML Snippet...")
                content = page.content()
                # پورا HTML بہت بڑا ہوگا، صرف باڈی کا کچھ حصہ پرنٹ کر رہے ہیں
                print_debug("--- HTML START ---")
                print_debug(content[:2000]) # پہلے 2000 الفاظ
                print_debug("--- HTML END ---")
                
                # Check for Captcha Keywords
                if "verify" in content.lower() or "captcha" in content.lower():
                    print_debug("⚠️ CAPTCHA DETECTED ON PAGE!")
                if "login" in content.lower():
                    print_debug("⚠️ LOGIN WALL DETECTED!")

        except Exception as e:
            print_debug(f"🔥 CRITICAL ERROR: {str(e)}")
        finally:
            if 'browser' in locals():
                browser.close()
                print_debug("Browser Closed.")

    # Final Output for Go
    print(json.dumps(results))

if __name__ == "__main__":
    query = "funny"
    if len(sys.argv) > 1:
        query = sys.argv[1]
    search_tiktok(query)