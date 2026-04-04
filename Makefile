VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test vet clean install

build:
	go build -ldflags "$(LDFLAGS)" -o bin/rivet ./cmd/rivet

test:
	go test ./... -count=1

vet:
	go vet ./...

clean:
	rm -rf bin/ dist/

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/rivet
