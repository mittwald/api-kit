// Package cli builds the command tree of a command-line tool and runs it.
//
// A host binary describes itself in [Options] and calls [Run] with its
// arguments; the exit code comes back. Package cli is the only package of
// api-kit that writes to the terminal: it prints results to the output
// stream, and errors, warnings and notices to the error stream.
//
// Errors are printed with an "ERROR: " prefix. A mistake on the command line
// ends with exit code 2 and a hint at the command's help; the other exit
// codes are those of [apikit.Kind].
package cli
