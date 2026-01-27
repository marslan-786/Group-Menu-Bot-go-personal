import sys
import json
import time
from playwright.sync_api import sync_playwright

def print_debug(msg):
    sys.stderr.write(f"🐍 [DEBUG] {msg}\n")
    sys.stderr.flush()

def search_tiktok(query, limit=10):
    results = []
    print_debug(f"🚀 Starting Network Sniffer for: {query}")

    with sync_playwright() as p:
        try:
            # 1. Browser Setup (With Network Monitoring)
            browser = p.chromium.launch(
                headless=True,
                args=[
                    "--no-sandbox",
                    "--disable-gpu",
                    "--disable-blink-features=AutomationControlled"
                ]
            )
            
            context = browser.new_context(
                user_agent="Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
                viewport={"width": 1920, "height": 1080}
            )
            
            page = context.new_page()

            # 2. 🔥 NETWORK INTERCEPTOR (The Magic Part)
            # ہم ہر آنے والی ریسپانس کو چیک کریں گے
            def handle_response(response):
                try:
                    # اگر ریسپانس JSON ہے اور اس میں ویڈیوز ہیں
                    if "item_list" in response.url or "search_item" in response.url or "video" in response.url:
                        # کبھی کبھی TikTok سیدھا HTML میں ڈیٹا بھیجتا ہے، کبھی JSON میں
                        # ہم فی الحال آسان طریقہ آزماتے ہیں: HTML سے لنکس نکالنا (Backup)
                        pass
                except:
                    pass

            page.on("response", handle_response)

            # 3. Navigate
            if query.startswith("#"):
                url = f"https://www.tiktok.com/tag/{query[1:]}"
            else:
                url = f"https://www.tiktok.com/search?q={query}"

            print_debug(f"Navigating to: {url}")
            page.goto(url, timeout=60000, wait_until="domcontentloaded")

            # 4. Scroll to trigger XHR requests
            print_debug("Scrolling to fetch data...")
            for _ in range(5):
                page.keyboard.press("End")
                time.sleep(2)

            # 5. 💪 BRUTE FORCE EXTRACTION (Updated Selectors)
            # TikTok اب لنکس کو چھپاتا ہے، اس لیے ہم ہر چیز کو scan کریں گے
            print_debug("Extracting video objects...")
            
            data = page.evaluate("""
                () => {
                    const items = [];
                    // TikTok Universal Video Containers
                    // یہ وہ کلاسز ہیں جو اکثر ویڈیوز پر ہوتی ہیں
                    const candidates = document.querySelectorAll('div[data-e2e="search_top_item"], div[data-e2e="search_item"], a');
                    
                    candidates.forEach(el => {
                        // لنک ڈھونڈیں
                        let link = el.getAttribute('href');
                        if (!link && el.tagName === 'DIV') {
                            const a = el.querySelector('a');
                            if (a) link = a.getAttribute('href');
                        }

                        // اگر لنک ویڈیو کا ہے
                        if (link && link.includes('/video/')) {
                            // ٹائٹل نکالیں
                            let title = el.innerText || "";
                            const img = el.querySelector('img');
                            if (img && img.alt && img.alt.length > title.length) title = img.alt;
                            
                            // Absolute URL بنائیں
                            if (link.startsWith('/')) link = "https://www.tiktok.com" + link;

                            // صفائی اور پش
                            title = title.replace(/\\n/g, ' ').trim();
                            if (title.length > 80) title = title.substring(0, 77) + "...";
                            if (!title) title = "TikTok Video";

                            // Duplicate Check
                            if (!items.find(i => i.url === link)) {
                                items.push({ title: title, url: link });
                            }
                        }
                    });
                    return items;
                }
            """)
            
            # فلٹر کریں (کیونکہ کبھی کبھی یوزر پروفائل لنکس بھی آ جاتے ہیں)
            filtered_results = [item for item in data if "/video/" in item['url']]
            
            print_debug(f"Found {len(filtered_results)} valid videos.")
            results = filtered_results[:limit]

        except Exception as e:
            print_debug(f"🔥 Error: {str(e)}")
        finally:
            if 'browser' in locals():
                browser.close()

    # Final JSON Output
    print(json.dumps(results))

if __name__ == "__main__":
    query = "funny"
    if len(sys.argv) > 1:
        query = sys.argv[1]
    search_tiktok(query)