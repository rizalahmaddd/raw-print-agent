VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: test build windows run clean

test:
	go vet ./...
	GOOS=windows go vet ./...
	go test -race ./...

build:
	go build -ldflags "$(LDFLAGS)" -o dist/rawprint-agent ./cmd/rawprint-agent

windows:
	GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/rawprint-agent-amd64.exe ./cmd/rawprint-agent
	GOOS=windows GOARCH=386 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/rawprint-agent-386.exe ./cmd/rawprint-agent

# Development without a printer: jobs are written to ./out as .prn files.
run:
	go run ./cmd/rawprint-agent run -config ./dev/config.json -file-spool ./out -file-printers "EPSON LX-310 ESC/P,Microsoft Print to PDF"

clean:
	rm -rf dist out
