---
title: Math & Geometry
---

# Math & Geometry

This group has no single algorithm. What it has is a habit: **find the structure in the indices**, and the loop writes itself. Rotating a matrix, spiralling through one, or detecting a cycle in digit sums are all about noticing the relationship rather than inventing a technique.

Two ideas recur. Matrix problems become easy once you pick the right coordinate transform — a rotation is a transpose plus a row reversal, and that is much easier to get right than juggling four indices. Number problems usually turn on digit extraction (`n % 10`, `n / 10`) and on watching for overflow.

## When to reach for it

- Rotate, transpose, spiral, or zero out rows and columns of a matrix.
- Digit manipulation: reverse an integer, happy number, palindrome number.
- Fast exponentiation, multiplication or division without the operator.
- Counting primes, counting trailing zeroes, factorials.
- "Do it in place with `O(1)` extra space" on a matrix.

## The shape

```go
// Rotate 90° clockwise = transpose, then reverse each row.
// Two simple passes beat one clever four-way swap.
for i := range matrix {
    for j := i + 1; j < len(matrix); j++ { // j > i: upper triangle only
        matrix[i][j], matrix[j][i] = matrix[j][i], matrix[i][j]
    }
}
for _, row := range matrix {
    slices.Reverse(row)
}
```

```go
// Spiral: four shrinking boundaries, and check them before the last two passes.
top, bottom, left, right := 0, len(m)-1, 0, len(m[0])-1
for top <= bottom && left <= right {
    for c := left; c <= right; c++ {
        out = append(out, m[top][c])
    }
    top++
    for r := top; r <= bottom; r++ {
        out = append(out, m[r][right])
    }
    right--
    if top <= bottom { // a single remaining row must not be read twice
        for c := right; c >= left; c-- {
            out = append(out, m[bottom][c])
        }
        bottom--
    }
    if left <= right {
        for r := bottom; r >= top; r-- {
            out = append(out, m[r][left])
        }
        left++
    }
}
```

```go
// Using the matrix itself as the marker store, for O(1) extra space.
// Row 0 and column 0 record which rows and columns must be zeroed;
// their overlap needs one separate flag.
firstColZero := false
for r := range m {
    if m[r][0] == 0 {
        firstColZero = true
    }
    for c := 1; c < len(m[0]); c++ {
        if m[r][c] == 0 {
            m[r][0], m[0][c] = 0, 0
        }
    }
}
// Then apply, iterating backwards so the markers survive until read.
```

```go
// Digits, and the overflow check that goes with reversing an integer.
for n != 0 {
    digit := n % 10
    n /= 10
    if out > math.MaxInt32/10 || (out == math.MaxInt32/10 && digit > 7) {
        return 0
    }
    out = out*10 + digit
}
```

```go
// Fast exponentiation: halve the exponent instead of decrementing it.
func myPow(x float64, n int) float64 {
    if n < 0 {
        x, n = 1/x, -n
    }
    result := 1.0
    for n > 0 {
        if n&1 == 1 {
            result *= x
        }
        x *= x
        n >>= 1
    }
    return result
}
```

## Complexity

Matrix passes are `O(m · n)` and often `O(1)` extra space. Digit work is `O(log n)` — one step per digit. Fast exponentiation is `O(log n)` instead of `O(n)`. The Sieve of Eratosthenes is `O(n log log n)`.

## Go mechanics

- `slices.Reverse` reverses in place, which is exactly what the rotation needs.
- `a, b = b, a` swaps without a temporary, and with two indices it reads cleanly.
- Go's `%` keeps the sign of the dividend: `-7 % 10` is `-3`, not `7`. Take `math.Abs` or handle the sign up front.
- `int` is 64-bit here, so an `int32` overflow problem has to be checked against `math.MaxInt32` deliberately — the hardware will not do it for you.
- Integer division truncates toward zero. Ceiling division is `(a + b - 1) / b`.
- `n>>1` and `n&1` instead of `n/2` and `n%2` for exponentiation: same result, and it says "I am working in binary".
- Transposing with `j := i + 1` touches each pair once. Starting from `j := 0` swaps everything twice and leaves the matrix unchanged.

## Where it goes wrong

- Transposing over the whole matrix instead of one triangle, which undoes itself.
- Spiral traversal re-reading the middle row or column of an odd-sized matrix, from a missing boundary check.
- Assuming `%` on a negative number returns a positive remainder.
- Checking for overflow *after* the multiply, when the value is already wrong.
- Writing markers into row 0 and column 0 and then reading them after they have been overwritten — the application pass must run backwards.
- Reading `m[0]` for the width without checking the matrix has any rows.
