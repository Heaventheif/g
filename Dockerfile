# ═══════════════════════════════════════════════════════════════════════════
#  SunkenBot — ملف Go تنفيذي واحد (بلا Python)
# ═══════════════════════════════════════════════════════════════════════════
#
#  Render ──► Go : المنفذ 7860 (HF_SPACE_URL)
#               ├─ gemini, groq, comic, novel, sub, manga-bridge, pinterest, ping
#               └─ chess (/process_move): قواعد + رسم + Stockfish — كله داخل Go
#
#  متغيرات البيئة:
#    HF_SPACE_URL     — رابط الفضاء الخارجي (يستخدمه SunkenBot فقط)
#    INTERNAL_TOKEN   — توكن الحماية بين Render↔Go
#    الشطرنج (اختيارية): STOCKFISH_PATH, STOCKFISH_POOL, STOCKFISH_HASH,
#      STOCKFISH_THREADS, LICHESS_TOKEN, LICHESS_TIMEOUT_MS, LICHESS_MAX_FULLMOVE
#
# ═══════════════════════════════════════════════════════════════════════════

# ════ المرحلة 1: بناء Go ═══════════════════════════════════════════════════
FROM golang:1.26-alpine AS build
WORKDIR /app
RUN apk add --no-cache git ca-certificates
COPY go.mod go.sum ./
COPY . .
# go mod tidy داخل مرحلة البناء (كما كان في الأصل): حزمة جديدة في أي
# import يُولَّد لها سطر go.sum صحيح تلقائياً وقت البناء في بيئة HF.
RUN go mod tidy
RUN CGO_ENABLED=0 GOOS=linux go build -tags postgres -trimpath -ldflags="-s -w" -o /sunkenbot .

# ════ المرحلة 2: التشغيل ═══════════════════════════════════════════════════
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates curl \
        # Go services
        chromium ffmpeg \
        # محرك الشطرنج
        stockfish \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd -r sunkenbot && useradd -r -g sunkenbot -d /app sunkenbot

# stockfish من مستودعات Debian يُثبَّت في /usr/games غير المدرج في PATH
# الافتراضي (لا تحذف هذا السطر).
ENV PATH="${PATH}:/usr/games"

# ─── Piper Arabic TTS (احتياطي لـ Groq): ملف تنفيذي مستقل بلا Python ─────────
# النموذج داخل الصورة ليبقى التشغيل محلياً ولا يعتمد على خدمة خارجية.
# تحذير: روابط التنزيل أدناه لم تُجرَّب في بيئة التطوير — تحقق من أول بناء.
RUN mkdir -p /opt/piper \
    && curl -fsSL https://github.com/rhasspy/piper/releases/download/2023.11.14-2/piper_linux_x86_64.tar.gz \
       | tar -xz -C /opt \
    && ln -s /opt/piper/piper /usr/local/bin/piper \
    && curl -fsSL -o /opt/piper/ar_JO-kareem-medium.onnx \
       https://huggingface.co/rhasspy/piper-voices/resolve/v1.0.0/ar/ar_JO/kareem/medium/ar_JO-kareem-medium.onnx \
    && curl -fsSL -o /opt/piper/ar_JO-kareem-medium.onnx.json \
       https://huggingface.co/rhasspy/piper-voices/resolve/v1.0.0/ar/ar_JO/kareem/medium/ar_JO-kareem-medium.onnx.json

WORKDIR /app
COPY --from=build /sunkenbot /app/sunkenbot

# مجلد صور manga-bridge (plugins/mangabridge يكتب هنا)
RUN mkdir -p /app/data/manga_bridge/images && chown -R sunkenbot:sunkenbot /app

USER sunkenbot
# غير-root آمن: internal/browser يمرر chromedp.NoSandbox دائماً.

EXPOSE 7860
ENV PORT=7860
HEALTHCHECK --interval=30s --timeout=3s --start-period=15s --retries=3 \
    CMD curl -fsS "http://localhost:${PORT}/health" || exit 1
CMD ["/app/sunkenbot"]
