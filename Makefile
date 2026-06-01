SHELL = /bin/bash
TARGET = webscreenie
VERSION = 0.1.1
CGO_ENABLED = 0
GO_FILES := $(shell find . -name "*.go" -type f)
LDFLAGS := -X github.com/miku/webscreenie/cmd.version=$(VERSION)

.PHONY: all
all: $(TARGET)

$(TARGET): $(GO_FILES)
	CGO_ENABLED=$(CGO_ENABLED) go build -ldflags "$(LDFLAGS)" -o $@ .

.PHONY: test
test:
	CGO_ENABLED=$(CGO_ENABLED) go test -v ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: imports
imports:
	goimports -w .

.PHONY: clean
clean:
	rm -f $(TARGET)
	rm -f webscreenie*.png
