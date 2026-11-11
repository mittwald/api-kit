// Package apikit holds what every package of api-kit shares: the kinds of
// errors the engine reports and the exit codes a command-line tool maps them
// to.
//
// api-kit turns a declarative description of a REST API into a command-line
// tool. The packages below this one never print or exit; they return errors
// that carry a [Kind], and package cli decides what to print and which exit
// code to use.
package apikit
