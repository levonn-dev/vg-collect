// Package sharedmetric exists so lint.Run's testdata/repo fixture has
// a real libs/go/ directory (Known errors on a missing scan root - see
// names.go) even though none of this fixture's rules or panels need a
// second registered metric.
package sharedmetric
