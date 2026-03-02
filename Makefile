BINARY := remote-agent
LDFLAGS := -ldflags="-s -w"

.PHONY: build build-linux build-all clean vet

build:
	CGO_ENABLED=0 go build $(LDFLAGS) -o $(BINARY) .

build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY)-linux-amd64 .

build-all: clean
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY)-linux-amd64 .
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o dist/$(BINARY)-darwin-arm64 .

vet:
	go vet ./...

clean:
	rm -rf dist/ $(BINARY) $(BINARY)-linux-amd64 $(BINARY)-darwin-arm64
