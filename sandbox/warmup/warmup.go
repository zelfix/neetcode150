// Package warmup exists only to be compiled when the sandbox image is built.
//
// A cold build of a solution's test binary costs about four seconds and writes
// 75 MB of cache entries. Compiling a package of this exact shape at image
// build time — same module, same import graph, same /src path, since Go's cache
// keys are path-sensitive — means a real run links archives that are already
// there and finishes in well under a second.
package warmup
