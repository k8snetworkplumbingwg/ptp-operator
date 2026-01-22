GOLANGCI_VERSION=v2.8.0
GO_PACKAGES=$(shell go list ./... | grep -v vendor)

.PHONY: all clean test build build-l2discovery lint install-lint vet fmt image launch

all: build test

clean:
	go clean ./...

fmt:
	go fmt ./...

lint:
	golangci-lint run

# Install golangci-lint v2
install-lint:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_VERSION}

vet:
	go vet ${GO_PACKAGES}

# Build all packages except cmd/l2discovery (requires Linux headers for CGO)
build:
	go build $$(go list ./... | grep -v cmd/l2discovery)

# Build l2discovery binary (Linux only, requires CGO)
build-l2discovery:
	go build -o l2discovery ./cmd/l2discovery

test:
	./scripts/test.sh

image:
	./scripts/image.sh

launch:
	./scripts/launch.sh
