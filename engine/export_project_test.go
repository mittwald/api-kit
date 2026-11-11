package engine

// What follows hands the tests in package engine_test the rules of joins
// and links that are worth a table of their own.

// Quote escapes text for a URL the way [Engine.Link] does.
func Quote(text string) string { return quote(text) }

// CompareSortValues orders two values the way the sort of a join does.
func CompareSortValues(a, b any) int { return compareSortValues(a, b) }
