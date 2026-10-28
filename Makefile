APP_NAME := g
MODULE   := github.com/ducconit/gvm
VERSION  ?= 1.0.1
COMMIT   := $(shell git rev-parse --short HEAD 2>/dev/null || echo "dev")
DATE     := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS  := -s -w \
	-X $(MODULE)/internal/version.Version=$(VERSION) \
	-X $(MODULE)/internal/version.Commit=$(COMMIT) \
	-X $(MODULE)/internal/version.BuildDate=$(DATE)

.PHONY: all build test vet cross-build clean

all: vet test build

vet:
	go vet ./...

test:
	go test -v -race -count=1 ./...

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/$(APP_NAME) ./cmd/g

cross-build:
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/$(APP_NAME)-linux-amd64 ./cmd/g
	CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/$(APP_NAME)-linux-arm64 ./cmd/g
	CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/$(APP_NAME)-darwin-amd64 ./cmd/g
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/$(APP_NAME)-darwin-arm64 ./cmd/g
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/$(APP_NAME)-windows-amd64.exe ./cmd/g
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/$(APP_NAME)-windows-arm64.exe ./cmd/g

clean:
	rm -rf bin dist
