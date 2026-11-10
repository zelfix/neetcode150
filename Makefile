# The same checks CI runs, so a push is not the first time they are seen.
.PHONY: check fmt vet build test test-short lint tidy python-test python-syntax

check: fmt vet build test lint tidy python-syntax python-test

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

# The Python practice harness has no third-party tooling by design, so its
# checks are the interpreter's own: compile every file, run its unittest suite.
python-syntax:
	python3 -I -B -m compileall -q internal/testkit/py

python-test:
	python3 -I -B -m unittest discover -s internal/testkit/py/tests -t internal/testkit/py
