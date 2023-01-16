# Build sample plugin with debug info
# https://www.jetbrains.com/help/go/attach-to-running-go-processes-with-debugger.html
.PHONY: build-loki-plugin-debug
build-loki-plugin-debug:
	CGO_ENABLED=0 go build -gcflags="all=-N -l" -o loki-plugin main.go

.PHONY: build-loki-plugin
build-loki-plugin:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o loki-plugin-linux-amd64 main.go
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o loki-plugin-linux-arm64 main.go
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w" -o loki-plugin-darwin-amd64 main.go
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o loki-plugin-darwin-arm64 main.go

.PHONY: test-loki-plugin
test-loki-plugin:
	go test ./...