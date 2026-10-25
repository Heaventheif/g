// Package strutil يوفّر دوال مساعدة للنصوص مشتركة بين كل الـ plugins —
// بدل نسخها في كل حزمة (كانت truncate مكررة في 6 ملفات، وstringOr في 3، وfirstNonEmpty في ملف).
//
// truncate تعمل بالأحرف (runes) لا بالبايتات، مما يمنع قطع النصوص العربية
// أو أي نص Unicode متعدد البايتات في منتصف حرف.
package strutil

// Truncate يُقصّر النص s إلى n حرف (rune) كحدٍّ أقصى.
// آمنة للنصوص العربية وأي Unicode متعدد البايتات — خلافاً لـ s[:n] التي
// تقطع بالبايتات وقد تُعطب حروفاً متعددة البايتات.
func Truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

// StringOr يُعيد قيمة v كـ string إن كانت كذلك، وإلا يُعيد def.
// مفيد عند استخراج حقول من map[string]any.
func StringOr(v any, def string) string {
	if s, ok := v.(string); ok {
		return s
	}
	return def
}

// FirstNonEmpty يعيد أول قيمة غير فارغة، أو "" إن كانت كلها فارغة.
func FirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
