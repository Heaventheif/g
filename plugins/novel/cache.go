package novel

import (
	"time"

	"sunkenbot/internal/cache"
)

const (
	cacheTTL     = time.Hour
	cacheMaxSize = 300
)

func (s *Service) cacheStore() *cache.Cache[string, map[string]any] {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if s.cache == nil { // يحافظ على صلاحية القيمة الصفرية لـ Service في الاختبارات.
		s.cache = cache.New[string, map[string]any](cacheMaxSize, cache.FIFO, cacheTTL)
	}
	return s.cache
}

func (s *Service) cacheGet(key string) (map[string]any, bool) {
	return s.cacheStore().Get(key)
}

func (s *Service) cacheSet(key string, value map[string]any) {
	s.cacheStore().Set(key, value)
}

func (s *Service) cacheClear() int {
	return s.cacheStore().Clear()
}
