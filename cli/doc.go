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
//
// What goes to the error stream is for a person and often holds what an API
// answered: errors, warnings and the log of --verbose are written as plain
// text, without escape sequences and control characters, and with the
// credentials they can be seen to hold replaced by [REDACTED], such as the
// value of a "password" or "token" field, a Bearer token or a token whose
// prefix names its issuer (secret.RedactPatterns of cli-kit). Results on
// the output stream are left as the API returned them.
//
// On a terminal a long run shows what it is busy with on a status line
// below its output. The line is taken off the terminal before anything is
// written to either stream, and kept off while the user is asked for
// credentials, so it never runs into a warning or a prompt.
//
// --links[=WHEN] makes the first column of a table a link: auto (the
// default) where the output is a terminal and neither NO_COLOR nor
// TERM=dumb ask for output without escape sequences, always or never. A
// value that is none of these fails the command line before anything is
// sent. With [Options.CompletionInstall] the completion command of bash,
// zsh and fish takes --install, which writes the script where the shell
// loads it from.
package cli
