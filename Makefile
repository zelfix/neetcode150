# The same checks CI runs, so a push is not the first time they are seen.
.PHONY: check fmt vet build test test-short lint tidy

check: fmt vet build test lint tidy

fmt:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "not gofmt'd:"; echo "$$out"; exit 1; fi

vet:
	go vet ./...

build:
	go build ./...

test:
	go test -race -count=1 ./...

# Skips the 40 s suite that checks every practice fixture against its signature.
test-short:
	go test -race -short -count=1 ./...

# golangci-lint v2; `brew install golangci-lint` or see .golangci.yml for the set.
lint:
	golangci-lint run ./...

tidy:
	@go mod tidy && git diff --exit-code -- go.mod go.sum
