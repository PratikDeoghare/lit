// Command lit is the brashtag literate-programming toolkit: tangle, weave, absorb, sync and the rest, in one binary.
package main

import (
	"os"

	"github.com/pratikdeoghare/brashtag-literate/internal/lit"
)

func main() {
	os.Exit(lit.Main(os.Args[1:], lit.IO{In: os.Stdin, Out: os.Stdout, Err: os.Stderr, IsTTY: lit.IsTerminal(os.Stdin)}))
}
