package warmup

import (
	"testing"

	"neetcode150/internal/testbank"
	"neetcode150/internal/testkit"
)

// TestWarmth is never meant to run. Type-checking and compiling it is the whole
// point: it pulls testkit, testbank, testing and the standard library into the
// build cache, which is the set every generated practice test links against.
func TestWarmth(t *testing.T) {
	t.Skip("compiled to warm the sandbox image's build cache, not to assert anything")
	if _, err := testbank.Load("two-sum"); err != nil {
		t.Fatal(err)
	}
	testkit.Run(t, "", func() {})
}
