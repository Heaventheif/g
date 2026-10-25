package chess

// config.go — قراءة إعدادات البيئة.

import (
	"strconv"
)

func envInt(get func(string) string, key string, def int) int {
	if v := get(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
