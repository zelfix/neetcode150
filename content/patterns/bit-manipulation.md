---
title: Bit Manipulation
---

# Bit Manipulation

A handful of operators, used as a set. `&` tests and clears, `|` sets, `^` toggles, `<<` and `>>` shift. Once you read them as operations on a set of flags rather than arithmetic, these problems become short.

Two facts carry most of this group. **XOR is its own inverse**: `a ^ a == 0` and `a ^ 0 == a`, so XOR-ing a whole list cancels every value that appears twice and leaves the one that does not. And **`n & (n-1)` clears the lowest set bit**, which turns bit counting into a loop that runs once per set bit instead of once per bit.

## When to reach for it

- "Every element appears twice except one" — XOR the lot.
- Counting set bits, for one number or for every number up to `n`.
- Add, subtract or divide without the arithmetic operators.
- Reverse the bits of a number, or swap its halves.
- Subsets enumerated as the integers `0` to `2^n - 1`, each bit meaning "include this element".
- A missing number in `0..n`, which XOR finds without a sum that could overflow.

## The shape

```go
// XOR cancels pairs, leaving the single.
result := 0
for _, v := range nums {
    result ^= v
}
```

```go
// Missing number: XOR the indices against the values.
// Every present value cancels with its index; the missing one survives.
result := len(nums)
for i, v := range nums {
    result ^= i ^ v
}
```

```go
// Brian Kernighan: one iteration per set bit, not per bit.
count := 0
for n != 0 {
    n &= n - 1 // clears the lowest set bit
    count++
}
```

```go
// Counting bits for every i up to n, in one pass.
// i>>1 has the same bits as i, minus the last one.
dp := make([]int, n+1)
for i := 1; i <= n; i++ {
    dp[i] = dp[i>>1] + (i & 1)
}
```

```go
// Addition without +: XOR is the sum without carry, AND<<1 is the carry.
for b != 0 {
    carry := (a & b) << 1
    a ^= b
    b = carry
}
```

```go
// Reversing 32 bits: pull from the bottom, push onto the top.
var out uint32
for range 32 {
    out = out<<1 | (n & 1)
    n >>= 1
}
```

```go
// Subsets as bitmasks.
for mask := range 1 << len(nums) {
    subset := []int{}
    for i := range nums {
        if mask&(1<<i) != 0 {
            subset = append(subset, nums[i])
        }
    }
}
```

## Complexity

`O(n)` for a single XOR pass with `O(1)` space — which is the point, since the hash-set solution to the same problem is `O(n)` space. Bit counting is `O(number of set bits)` with Kernighan, versus `O(32)` naively. Subset enumeration is `O(n · 2^n)`.

## Go mechanics

- The operators: `&` and, `|` or, `^` xor, `&^` **and-not** (clear bits — a Go-specific operator worth knowing), `<<` and `>>` shifts.
- `^x` with one operand is bitwise **NOT**. Go has no `~`.
- **Right shift on a signed int is arithmetic**: it copies the sign bit, so `-8 >> 1` is `-4` and a loop on a negative number never terminates. Convert to `uint32`/`uint` for bit-pattern work. This is the single biggest trap here.
- `math/bits` has the whole toolkit: `bits.OnesCount32`, `bits.Reverse32`, `bits.LeadingZeros32`, `bits.TrailingZeros32`. Use them in real code; write the loop here because the loop is the exercise.
- `1 << i` is an `int`. For shifts near 32 or 64, make the type explicit: `uint32(1) << i`.
- `for mask := range 1 << n` works since Go 1.22 — ranging over an integer.
- Operator precedence around shifts is easy to misread. Parenthesise: `(n & 1)`, `mask&(1<<i)`.

## Where it goes wrong

- Shifting a signed negative number right and looping forever, or getting sign-extended garbage.
- Using `^` for exponentiation out of habit from other languages. It is XOR.
- `n & n-1` without parentheses — precedence makes this work by luck, not by design; write `n & (n - 1)`.
- Forgetting that `a ^ a == 0` only helps when every unwanted element appears an **even** number of times.
- Mixing `int` and `uint32` in one expression; Go will not implicitly convert, which is the compiler doing you a favour.
- Overflow in an addition-without-plus loop when the inputs are near the type's limit.
