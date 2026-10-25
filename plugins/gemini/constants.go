package gemini

// constants.go — ثوابت النماذج ومفاتيح API.

import (
	"sync"

	"sunkenbot/internal/keyrotate"
)

const (
	// نموذج الدردشة الرئيسي — أفضل موديل مجاني متاح حالياً.
	// Free Tier: 10 RPM / 250 RPD / 250k TPM
	chatModel = "gemini-3.6-flash"

	// Groq fallback — يُستخدم عند استنفاد كل مفاتيح Gemini
	// ملاحظة: llama-3.3-70b-versatile أوقفته Groq نهائياً — البديل الرسمي
	// الحالي الذي توصي به Groq هو openai/gpt-oss-120b.
	groqModel = "openai/gpt-oss-120b"

	systemPrompt = `أنت بوت مساعد ذكي اسمك "Sunken". أجب باللغة العربية بإيجاز (أقل من 200 كلمة).
كن ودوداً ومفيداً. عند السؤال عن أحداث جارية أو أخبار أو معلومات تتغير بمرور الوقت،
استخدم بحث Google للحصول على معلومات محدّثة وأذكر المصدر.`
)

// ─── مفاتيح البيئة ─────────────────────────────────────────────────

// geminiKeys يدير مجموعة مفاتيح Gemini (تدوير Round-Robin + تبديل تلقائي عند
// 429) — يُبنى مرة واحدة فقط. يدعم كلا النمطين معاً للتوافق الخلفي:
//   - عدة متغيرات بيئة منفصلة: GEMINI_API_KEY, GEMINI_API_KEY_2, _3, _4 (كما كانت)
//   - أو مفاتيح متعددة مفصولة بفواصل داخل أيٍّ من هذه المتغيرات (مثل
//     GEMINI_API_KEY=key1,key2,key3) — الأسلوب المفضّل للإضافة/الحذف السريع.
var geminiKeys = sync.OnceValue(func() *keyrotate.Manager {
	return keyrotate.FromEnvAny(
		"GEMINI_API_KEY", "GEMINI_API_KEY_2",
		"GEMINI_API_KEY_3", "GEMINI_API_KEY_4",
	)
})

// groqKeys يدير مجموعة مفاتيح Groq المستخدمة هنا فقط كخط احتياط (fallback)
// عند استنفاد كل مفاتيح Gemini — GROQ_API_KEY يقبل مفتاحاً واحداً أو عدة
// مفاتيح مفصولة بفواصل.
var groqKeys = sync.OnceValue(func() *keyrotate.Manager {
	return keyrotate.FromEnv("GROQ_API_KEY")
})
