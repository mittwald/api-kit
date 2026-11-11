GO ?= go
GOLANGCI_LINT ?= golangci-lint
# The oldest Go release api-kit supports; keep it in sync with go.mod.
GO_MIN ?= go1.27.0

.PHONY: all test race cover lint fmt vet cross test-min tidy-check check

all: check

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

cover:
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out | tail -1

lint:
	$(GOLANGCI_LINT) run ./...

fmt:
	$(GOLANGCI_LINT) fmt ./...

vet:
	$(GO) vet ./...

# Build every package for the platforms the kit supports.
cross:
	GOOS=linux $(GO) build ./...
	GOOS=darwin $(GO) build ./...
	GOOS=windows $(GO) build ./...
	GOOS=freebsd $(GO) build ./...

# Run the tests on the oldest supported Go release, so an API added in a
# newer standard library cannot slip in unnoticed.
test-min:
	GOTOOLCHAIN=$(GO_MIN) $(GO) test ./...

tidy-check:
	$(GO) mod tidy -diff

check: vet lint race cross tidy-check
