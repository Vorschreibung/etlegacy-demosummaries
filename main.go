package main

import (
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/spf13/cobra"
)

func main() {
	command := newRootCommand(os.Stdout, os.Stderr, runParser)
	command.SetArgs(normalizeOptionalIntFlags(os.Args[1:]))
	if err := command.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCommand(stdout io.Writer, stderr io.Writer,
	run func(io.Writer, io.Writer, parserOptions, []string) error) *cobra.Command {
	options := parserOptions{}

	command := &cobra.Command{
		Use:          "demoparser <demo.dm_84> [more demos...]",
		Short:        "Parse ET .dm_84 demos and print kill lines",
		SilenceUsage: true,
		Args:         cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if options.multiKillMin != 0 && options.multiKillMin < 2 {
				return fmt.Errorf("--multikills-only must be at least 2 when set")
			}
			if options.multiKillHeadshotMin != 0 && options.multiKillHeadshotMin < 2 {
				return fmt.Errorf("--multikill-headshots-only must be at least 2 when set")
			}
			if options.multiKillMin != 0 && options.multiKillHeadshotMin != 0 {
				return fmt.Errorf("--multikills-only and --multikill-headshots-only are mutually exclusive")
			}
			if options.killsOnlyFrom != "" {
				options.killsOnlyFrom = cleanName(options.killsOnlyFrom)
				if options.killsOnlyFrom == "" {
					return fmt.Errorf("--kills-only-from must not be empty")
				}
			}

			return run(stdout, stderr, options, args)
		},
	}

	command.SetOut(stdout)
	command.SetErr(stderr)

	flags := command.Flags()
	flags.IntVar(&options.multiKillMin, "multikills-only", 0,
		"only print multikill windows; when used without a value, require at least 2 kills per window")
	flags.Lookup("multikills-only").NoOptDefVal = "2"
	flags.IntVar(&options.multiKillHeadshotMin, "multikill-headshots-only", 0,
		"only print multikill windows made of headshot kills; when used without a value, require at least 2 kills per window")
	flags.Lookup("multikill-headshots-only").NoOptDefVal = "2"
	flags.StringVar(&options.killsOnlyFrom, "kills-only-from", "",
		"only print kills from the given cleaned player name")

	return command
}

func runParser(out io.Writer, warn io.Writer, options parserOptions, paths []string) error {
	for _, path := range paths {
		parser := newParserWithWarning(out, warn, options)
		if err := parser.parseFile(path); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}

	return nil
}

func normalizeOptionalIntFlags(args []string) []string {
	normalized := make([]string, 0, len(args))

	for i := 0; i < len(args); i++ {
		current := args[i]
		if !isOptionalIntFlag(current) || i+1 >= len(args) {
			normalized = append(normalized, current)
			continue
		}
		if _, err := strconv.Atoi(args[i+1]); err != nil {
			normalized = append(normalized, current)
			continue
		}

		normalized = append(normalized, current+"="+args[i+1])
		i++
	}

	return normalized
}

func isOptionalIntFlag(flag string) bool {
	switch flag {
	case "--multikills-only", "--multikill-headshots-only":
		return true
	default:
		return false
	}
}
