# أوامر التطوير المعتادة — نفس ما تشغّله بوابة CI قبل النشر.
.PHONY: fmt vet test run check

fmt:    ## تنسيق الكود
	gofmt -w .

vet:    ## فحص ثابت (مع وبدون postgres)
	go vet ./... && go vet -tags postgres ./...

test:   ## كل الاختبارات مع كاشف السباقات
	go test -race ./...

check: fmt vet test   ## كل شيء قبل الـ commit

run:    ## تشغيل محلي على المنفذ 7860
	go run -tags postgres .
