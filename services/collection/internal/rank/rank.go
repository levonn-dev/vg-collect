// Package rank generates lexicographically ordered fractional-index keys for
// backlog ordering. Keys are non-empty strings over 'a'..'z' that never end in
// 'a' (the minimum digit), so a strictly-between key always exists: a reorder
// is one single-row write. Postgres compares byte-wise (COLLATE "C"), matching Go string comparison.
package rank

import (
	"errors"
	"fmt"
	"strings"
)

const digits = "abcdefghijklmnopqrstuvwxyz"

var errMalformed = errors.New("rank: malformed key")

// Between returns a key strictly between prev and next in byte order. Empty
// prev/next means before/after everything; both empty yields the initial key.
// Errors when a non-empty key is malformed or prev is not strictly below next.
func Between(prev, next string) (string, error) {
	if err := validate(prev); err != nil {
		return "", err
	}
	if err := validate(next); err != nil {
		return "", err
	}
	if prev != "" && next != "" && prev >= next {
		return "", fmt.Errorf("rank: %q is not below %q", prev, next)
	}
	return midpoint(prev, next), nil
}

func validate(key string) error {
	if key == "" {
		return nil
	}
	for i := 0; i < len(key); i++ {
		if strings.IndexByte(digits, key[i]) < 0 {
			return fmt.Errorf("%w: %q", errMalformed, key)
		}
	}
	if key[len(key)-1] == digits[0] {
		return fmt.Errorf("%w: %q ends in the minimum digit", errMalformed, key)
	}
	return nil
}

// midpoint implements the classic digit-string midpoint: with a < b (empty b
// as upper infinity) it returns m with a < m < b, never ending in the minimum digit.
func midpoint(a, b string) string {
	if b != "" {
		// Consume the shared prefix, treating a as padded with minimum
		// digits, and recurse on the remainders.
		n := 0
		for n < len(b) {
			ca := digits[0]
			if n < len(a) {
				ca = a[n]
			}
			if ca != b[n] {
				break
			}
			n++
		}
		if n > 0 {
			return b[:n] + midpoint(tail(a, n), b[n:])
		}
	}
	// The first digits differ (or a bound is absent).
	da := 0
	if a != "" {
		da = strings.IndexByte(digits, a[0])
	}
	db := len(digits)
	if b != "" {
		db = strings.IndexByte(digits, b[0])
	}
	if db-da > 1 {
		// Any digit strictly between works; round the midpoint up so
		// the result index is at least 1 (never the minimum digit).
		return string(digits[(da+db+1)/2])
	}
	// Consecutive first digits: either b's own head already sits
	// strictly between (b is longer than one digit), or extend a.
	if len(b) > 1 {
		return b[:1]
	}
	return string(digits[da]) + midpoint(tail(a, 1), "")
}

func tail(s string, n int) string {
	if n >= len(s) {
		return ""
	}
	return s[n:]
}
