package main

import (
	"strings"
	"testing"
)

func TestServicesExcludeRemovedSubtitlesPlugin(t *testing.T) {
	for _, service := range servicesUsing(nil) {
		if service.Name() == "sub" {
			t.Fatal("removed subtitle plugin was registered")
		}
		for _, route := range service.Routes() {
			if strings.HasPrefix(route.Pattern, "/subtitler/") {
				t.Fatalf("removed subtitle route was registered: %s", route.Pattern)
			}
		}
	}
}
