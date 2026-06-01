SHELL = /bin/bash
TARGET = webscreenie
CGO_ENABLED = 0
GO_FILES := $(shell find . -name "*.go" -type f)

.PHONY: all
all: $(TARGET)

$(TARGET): $(GO_FILES)
	CGO_ENABLED=$(CGO_ENABLED) go build -o $@ .

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
