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
             ├─ gemini, groq, comic, novel, sub, manga-bridge, pinterest, ping
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
go test ./plugins/chess        # perft + مطابقة python-chess (testdata/positions.json) + الرسم + البوت
```

## إضافة خدمة جديدة

حزمة جديدة تحت `plugins/` تلتزم بعقد `plugins.Service` (`Name()` + `Routes()`) + سطر واحد في `services()` داخل `main.go` وسطر في `descriptions`. راجع `plugins/ping` كمثال.

## متغيرات البيئة العامة

| المتغير | الاستخدام |
|---|---|
| `INTERNAL_TOKEN` | توكن الحماية بين Render وGo |
| `PORT` | المنفذ (الافتراضي 7860) |

`GET /health` فحص صحة سريع.
