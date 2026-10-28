---
title: SunkenBot — البوابة الموحّدة
emoji: 🐙
colorFrom: blue
colorTo: purple
sdk: docker
app_port: 7860
pinned: false
---

# g — بوابة SunkenBot (Go فقط)

ملف Go تنفيذي واحد على المنفذ **7860**. لا Python ولا OCR ولا عمليات داخلية: كل الخدمات تعمل داخل نفس العملية، و**SunkenBot** يكلّم `HF_SPACE_URL` فقط.

## البنية

```
Render ──► Go (g) ── المنفذ 7860
             ├─ gemini, groq, comic, novel, manga-bridge, pinterest, ping
             └─ chess  (POST /process_move)
                  ├─ position.go : قواعد الشطرنج (توليد النقلات، FEN، نهاية اللعبة)
                  ├─ engine.go   : مجمّع عمليات Stockfish الدائمة + Lichess Cloud Eval
                  └─ render.go   : رسم الرقعة PNG من أصول مضمّنة (assets/)
```

## الشطرنج

`POST /process_move` بجسم `{"fen", "move"?, "bot_mode"?, "difficulty"?, "perspective"?}` ويعيد
`{"new_fen", "image_base64", "game_over", "winner"}` — نفس العقد السابق. تسلسل نقلة البوت: Lichess (كاش + مهلة قصيرة) ← Stockfish ← نقلة عشوائية قانونية.

| متغير بيئة | الافتراضي | المعنى |
|---|---|---|
| `STOCKFISH_PATH` | `stockfish` | مسار المحرك |
| `STOCKFISH_POOL` | `min(2, nCPU)` | أقصى عدد عمليات متزامنة |
| `STOCKFISH_HASH` / `STOCKFISH_THREADS` | `32` / `1` | إعدادات المحرك |
| `LICHESS_TOKEN` | — | اختياري |
| `LICHESS_TIMEOUT_MS` | `3000` | مهلة Lichess |
| `LICHESS_MAX_FULLMOVE` | `0` (دائماً) | لا تسأل Lichess بعد هذه النقلة |

### الاختبار

```bash
go test ./plugins/chess        # perft + مطابقة python-chess (testdata/positions/*.json) + الرسم + البوت
```

## إضافة خدمة جديدة

حزمة جديدة تحت `plugins/` تلتزم بعقد `plugins.Service` (`Name()` + `Routes()`) + سطر واحد في `services()` داخل `main.go` وسطر في `descriptions`. راجع `plugins/ping` كمثال.

## خريطة الملفات

كل حزمة مقسّمة حسب المسؤولية (اسم الملف يدل على محتواه، وأول سطر فيه يشرحه):

| الحزمة | الملفات |
|---|---|
| `internal/browser` | `browser.go` (إعداد/User-Agent) · `stealth.go` · `behavior.go` · `cloudflare.go` · `scrape.go` |
| `internal/tts` | `tts.go` (Orpheus العربي المشترك بين الخدمات) |
| `plugins/chess` | `chess.go` (المسار) · `position.go` · `moves.go` · `attacks.go` · `outcome.go` · `engine.go` · `lichess.go` · `cache.go` · `render.go` · `png.go` · `config.go` |
| `plugins/mangabridge` | `mangabridge.go` (المسارات) · `job.go` · `scraper.go` · `images.go` · `validation.go` |
| `plugins/pinterest` | `pinterest.go` · `scraper.go` · `transform.go` · `ferdev.go` |
| `plugins/novel` | `novel.go` · `cache.go` · `html.go` · `extract.go` · `fetch.go` · `slug.go` · `novelfull.go` |
| `plugins/groq` | `groq.go` · `chat.go` · `vision.go` · `search.go` · `tts.go` · `attachment.go` · `stt.go` |
| `plugins/gemini` | `gemini.go` · `constants.go` · `fallback.go` · `vision.go` · `vision_call.go` · `stt.go` · `tts.go` |

## التطوير والصيانة

```bash
make check     # تنسيق + vet + اختبارات (نفس بوابة CI)
make run       # تشغيل محلي
```

- **حدود الحزم:** لا تستورد أي حزمة تحت `plugins/` حزمة أخرى تحت `plugins/` (بما فيها Gemini وGroq). ضع التنفيذ المشترك في `internal/`؛ ويفرض `internal/plugins/import_boundary_test.go` هذا الحد آلياً. تشمل الحزم المشتركة `httpx` و`strutil` و`netguard` و`keyrotate` و`session` و`tts`.
- **النصوص:** استعمل `strutil.Truncate` لا `s[:n]` (الأخيرة تقطع الحروف العربية في منتصفها).
- **الإيقاف السليم:** إن كانت خدمتك تملك موارد (عمليات، اتصالات) فأضف `Close() error` — يستدعيه `main` تلقائياً عند SIGTERM عبر `io.Closer` دون تغيير عقد `plugins.Service`.
- **النشر:** كل push على `main` يشغّل `test` أولاً، ولا يُدفع إلى HF إلا إن نجح.

## متغيرات البيئة العامة

| المتغير | الاستخدام |
|---|---|
| `INTERNAL_TOKEN` | توكن الحماية بين Render وGo |
| `PORT` | المنفذ (الافتراضي 7860) |

`GET /health` فحص صحة سريع.
