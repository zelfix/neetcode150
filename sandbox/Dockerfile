# The sandbox that runs one person's `go test`.
#
# Built from the repository root:
#   docker build -t nc-sandbox:go1.26 -f sandbox/Dockerfile .
#
# Two things this image deliberately leaves out.
#
# The rest of the repository: a guest writes their own Go, so if
# internal/studydb were reachable they could import it and read every other
# person's notes, progress and archived code. Only internal/testkit,
# internal/testbank and the one package being tested are ever present.
#
# The project's real go.mod, and with it every third-party module. The packages
# that run here import nothing but the standard library, so the sandbox carries
# a go.mod of its own declaring the module path and nothing else. That needs no
# proxy at build time and no network at run time — and it makes the repository's
# own rule that solutions depend on nothing a fact of the toolchain rather than
# a request, with a comprehensible error when it is broken.

FROM golang:1.26-bookworm AS warm
ENV CGO_ENABLED=0 GOCACHE=/gocache-warm GOTOOLCHAIN=local GOPROXY=off GOFLAGS=-mod=mod
WORKDIR /src
# The module path has to be neetcode150: Go's internal-package rule is an
# import-path prefix test, so internal/testkit is only importable from under it.
RUN printf 'module neetcode150\n\ngo 1.26.0\n' > go.mod
COPY internal/testkit internal/testkit
COPY internal/testbank internal/testbank
COPY sandbox/warmup sandbox/warmup
# Compiling a package shaped like a generated practice test, at the same /src
# path a real run uses, leaves its dependency archives in the cache. Go's cache
# keys are path-sensitive, which is why the path matches rather than being
# anywhere convenient.
RUN go test -count=1 -run=^$ ./sandbox/warmup/

FROM golang:1.26-bookworm
WORKDIR /src
RUN printf 'module neetcode150\n\ngo 1.26.0\n' > /src/go.mod
COPY --from=warm /gocache-warm /gocache-warm
COPY --from=warm /src/internal /src/internal
COPY sandbox/entrypoint.sh /usr/local/bin/nc-sandbox
RUN chmod 0755 /usr/local/bin/nc-sandbox \
 && chmod -R a+rX /gocache-warm /src
# GOTOOLCHAIN=local is not optional. go.mod asks for go 1.26.0, and with the
# network off a mismatched toolchain would try to download one and then report
# something about toolchains rather than about the user's code.
ENV CGO_ENABLED=0 GOTOOLCHAIN=local GOPROXY=off GOFLAGS=-mod=mod \
    GOCACHE=/gocache GOTMPDIR=/tmp TMPDIR=/tmp HOME=/tmp
ENTRYPOINT ["/usr/local/bin/nc-sandbox"]
