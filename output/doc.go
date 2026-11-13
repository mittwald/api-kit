// Package output writes the results of a command in the format the user
// asked for with --output.
//
// A [Spec] is a parsed --output value. The writers of this package take the
// items of a result and a stream; they know nothing about commands. Formats
// for people (tables) paint and shorten what they show when the stream is a
// terminal; formats for programs (JSON, YAML, lines of values) write the
// data as it is.
//
// The data is whatever the API returned, and no format lets it reach a
// terminal as an escape sequence. Tables for people show their values as
// plain text. JSON and YAML write control characters as escapes, and CSV,
// TSV and Markdown leave them out. The formats that write lines of values
// (columns, id, name, link, jsonpath) give a program each value byte for
// byte, and leave escape sequences and control characters out where the
// stream is a terminal, as ls(1) does with file names. A line break inside
// a value never breaks a line of columns, IDs, names or links apart.
package output
