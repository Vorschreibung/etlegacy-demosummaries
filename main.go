package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	multiKillsOnly := flag.Bool("multikills-only", false,
		"only print kills that are part of a same-killer chain with at most 3 seconds between kills")

	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(),
			"Usage: %s [--multikills-only] <demo.dm_84> [more demos...]\n",
			os.Args[0],
		)
	}

	flag.Parse()
	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}

	parser := newParser(os.Stdout, parserOptions{
		multiKillsOnly: *multiKillsOnly,
	})
	for _, path := range flag.Args() {
		if err := parser.parseFile(path); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			os.Exit(1)
		}
		parser.resetState()
	}
}
