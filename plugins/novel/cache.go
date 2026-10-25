package novel

// cache.go — كاش الفصول (TTL + حد أقصى).

import (
	"time"
)

// ─── Cache (تقابل _cache/_cache_get/_cache_set في بايثون) ───────────

type cacheItem struct {
	value   map[string]any
	expires time.Time
}

const cacheTTL = time.Hour
const cacheMaxSize = 300

func (s *Service) cacheGet(key string) (map[string]any, bool) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	item, ok := s.cache[key]
	if !ok {
		return nil, false
	}
	if time.Now().After(item.expires) {
		delete(s.cache, key)
		s.removeFromOrderLocked(key)
		return nil, false
	}
	return item.value, true
}

// removeFromOrderLocked يحذف key من s.cacheOrder إن وُجد. يجب استدعاؤها
// مع الاحتفاظ بـ s.cacheMu بالفعل (locked). مسح صريح من الشريحة عند كل
// إزالة من s.cache (سواء بانتهاء الصلاحية في cacheGet أو بالإخلاء في
// cacheSet) يمنع تراكم مفاتيح منتهية الصلاحية في cacheOrder إلى ما لا
// نهاية (تسرّب ذاكرة بطيء لكن غير محدود مع طول مدة التشغيل).
func (s *Service) removeFromOrderLocked(key string) {
	for i, k := range s.cacheOrder {
		if k == key {
			s.cacheOrder = append(s.cacheOrder[:i], s.cacheOrder[i+1:]...)
			return
		}
	}
}

func (s *Service) cacheSet(key string, value map[string]any) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if _, exists := s.cache[key]; !exists {
		// أخلِ أقدم المفاتيح حتى يعود حجم الخريطة تحت الحد الأقصى. حلقة
		// (وليس شرط واحد) لأن s.cacheOrder قد يحوي مفاتيح لم تعد موجودة
		// في s.cache أصلاً (أُزيلت سابقاً)، فتخطّيها ببساطة دون عدّها.
		for len(s.cache) >= cacheMaxSize && len(s.cacheOrder) > 0 {
			oldest := s.cacheOrder[0]
			s.cacheOrder = s.cacheOrder[1:]
			delete(s.cache, oldest)
		}
		s.cacheOrder = append(s.cacheOrder, key)
	}
	s.cache[key] = cacheItem{value: value, expires: time.Now().Add(cacheTTL)}
}

func (s *Service) cacheClear() int {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	n := len(s.cache)
	s.cache = map[string]cacheItem{}
	s.cacheOrder = nil
	return n
}
