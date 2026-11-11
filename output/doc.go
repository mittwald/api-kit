// Package output writes the results of a command in the format the user
// asked for with --output.
//
// A [Spec] is a parsed --output value. The writers of this package take the
// items of a result and a stream; they know nothing about commands. Formats
// for people (tables) paint and shorten what they show when the stream is a
// terminal; formats for programs (JSON, YAML, lines of values) write the
// data as it is.
package output
