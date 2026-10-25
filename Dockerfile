# ═══════════════════════════════════════════════════════════════════════════
#  SunkenBot — ملف Go تنفيذي واحد (بلا Python)
# ═══════════════════════════════════════════════════════════════════════════
#
#  Render ──► Go : المنفذ 7860 (HF_SPACE_URL)
#               ├─ gemini, groq, comic, novel, manga-bridge, pinterest, ping
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
# طبقة التبعيات أولاً: لا تُبطَل إلا عند تغيّر go.mod/go.sum (كاش طبقات فعّال).
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# -mod=readonly: البناء لا يعدّل go.mod/go.sum أبداً ⇒ بناء قابل للتكرار يلتزم بما
# هو مُلتزَم به في المستودع. تنظيف التبعيات مسؤولية المطوّر (go mod tidy محلياً)
# وتفرضه خطوة `go mod tidy -diff` في CI (راجع .github/workflows/sync_to_hf.yml).
RUN CGO_ENABLED=0 GOOS=linux go build -mod=readonly -tags postgres -trimpath -ldflags="-s -w" -o /sunkenbot .

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
# رابط GitHub للـ binary يردّ 200 (فُحص)؛ روابط HuggingFace لم تُجرَّب من بيئة
# المراجعة — تحقق منها في أول بناء.
# تثبيت sha256: مرّر قيم الملفات الثلاثة عند البناء (--build-arg PIPER_TAR_SHA256=...)
# أو ضعها هنا بعد حسابها من بناء موثوق (sha256sum). إن تُركت فارغة يمرّ البناء
# مع تحذير صريح — لا تعتمد على ذلك في الإنتاج.
ARG PIPER_TAR_SHA256=""
ARG PIPER_ONNX_SHA256=""
ARG PIPER_ONNX_JSON_SHA256=""
RUN set -eu; \
    mkdir -p /opt/piper /tmp/piper-dl; \
    curl -fsSL -o /tmp/piper-dl/piper.tar.gz \
        https://github.com/rhasspy/piper/releases/download/2023.11.14-2/piper_linux_x86_64.tar.gz; \
    curl -fsSL -o /opt/piper/ar_JO-kareem-medium.onnx \
        https://huggingface.co/rhasspy/piper-voices/resolve/v1.0.0/ar/ar_JO/kareem/medium/ar_JO-kareem-medium.onnx; \
    curl -fsSL -o /opt/piper/ar_JO-kareem-medium.onnx.json \
        https://huggingface.co/rhasspy/piper-voices/resolve/v1.0.0/ar/ar_JO/kareem/medium/ar_JO-kareem-medium.onnx.json; \
    verify() { \
        if [ -n "$1" ]; then echo "$1  $2" | sha256sum -c -; \
        else echo "WARNING: لا sha256 مثبَّت لـ $2 — $(sha256sum "$2" | cut -d' ' -f1)"; fi; \
    }; \
    verify "$PIPER_TAR_SHA256" /tmp/piper-dl/piper.tar.gz; \
    verify "$PIPER_ONNX_SHA256" /opt/piper/ar_JO-kareem-medium.onnx; \
    verify "$PIPER_ONNX_JSON_SHA256" /opt/piper/ar_JO-kareem-medium.onnx.json; \
    tar -xz -C /opt -f /tmp/piper-dl/piper.tar.gz; \
    rm -rf /tmp/piper-dl; \
    ln -s /opt/piper/piper /usr/local/bin/piper

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
