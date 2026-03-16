package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

func main() {
	if err := executeCLI(os.Stdout, os.Stderr, os.Args[1:], runParser); err != nil {
		os.Exit(1)
	}
}

func executeCLI(stdout io.Writer, stderr io.Writer, args []string,
	run func(io.Writer, io.Writer, parserOptions, []string) error) error {
	command := newRootCommand(stdout, stderr, run)
	command.SetArgs(normalizeOptionalIntFlags(args))

	executed, err := command.ExecuteC()
	if err == nil {
		return nil
	}

	fmt.Fprintln(stderr, err)

	helpCommand := executed
	if helpCommand == nil {
		helpCommand = command
	}
	helpCommand.SetOut(stderr)
	if helpErr := helpCommand.Help(); helpErr != nil {
		return fmt.Errorf("%w: show help: %v", err, helpErr)
	}

	return err
}

func newRootCommand(stdout io.Writer, stderr io.Writer,
	run func(io.Writer, io.Writer, parserOptions, []string) error) *cobra.Command {
	options := parserOptions{multiKillWindow: 3}

	command := &cobra.Command{
		Use:          "etlegacy-demosummaries [flags] <demo.dm_84|demo.tv_84> [more demos...]",
		Short:        "Parse ET .dm_84 and .tv_84 demos and print kill lines",
		SilenceUsage: true,
		Args:         cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if options.multiKillMin != 0 && options.multiKillMin < 2 {
				return fmt.Errorf("--multikills-only must be at least 2 when set")
			}
			if options.multiKillHeadshotMin != 0 && options.multiKillHeadshotMin < 2 {
				return fmt.Errorf("--multikill-headshots-only must be at least 2 when set")
			}
			if options.multiKillWindow < 1 {
				return fmt.Errorf("--multikill-window must be at least 1")
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
	flags.IntVar(&options.multiKillWindow, "multikill-window", options.multiKillWindow,
		"seconds allowed between kills for them to count as the same multikill window")
	flags.StringVar(&options.killsOnlyFrom, "kills-only-from", "",
		"only print kills from the given cleaned player name")

	command.AddCommand(newSplitMultikillCommand(stdout, stderr))

	return command
}

func newSplitMultikillCommand(stdout io.Writer, stderr io.Writer) *cobra.Command {
	options := splitOptions{
		minimum:         2,
		multiKillWindow: 3,
		beforeSecs:      5,
		afterSecs:       5,
	}

	command := &cobra.Command{
		Use:          "split-multikill <demo.dm_84|demo.tv_84> [more demos...]",
		Aliases:      []string{"split-multikills"},
		Short:        "Split demos and TV demos into multikill clips",
		SilenceUsage: true,
		Args:         cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if options.minimum < 2 {
				return fmt.Errorf("--minimum must be at least 2")
			}
			if options.multiKillWindow < 1 {
				return fmt.Errorf("--multikill-window must be at least 1")
			}
			if options.beforeSecs < 0 {
				return fmt.Errorf("--before must be non-negative")
			}
			if options.afterSecs < 0 {
				return fmt.Errorf("--after must be non-negative")
			}

			return runSplitMultikill(stdout, stderr, options, args)
		},
	}

	command.SetOut(stdout)
	command.SetErr(stderr)

	flags := command.Flags()
	flags.IntVar(&options.minimum, "minimum", options.minimum,
		"minimum kills required inside the multikill window")
	flags.IntVar(&options.multiKillWindow, "multikill-window", options.multiKillWindow,
		"seconds allowed between kills for them to count as the same multikill window")
	flags.IntVar(&options.beforeSecs, "before", options.beforeSecs,
		"seconds to include before the multikill window")
	flags.IntVar(&options.afterSecs, "after", options.afterSecs,
		"seconds to include after the multikill window")
	flags.BoolVar(&options.fromMe, "from-me", false,
		"only split multikills done by the client who recorded the demo")
	flags.BoolVar(&options.filterKillerDying, "filter-killer-dying", false,
		"skip clips where the killer dies during the multikill itself")
	flags.BoolVar(&options.convertToDM84, "convert-to-dm-84", false,
		"when splitting .tv_84 demos, write the output clips as .dm_84 files")

	return command
}

func runParser(out io.Writer, warn io.Writer, options parserOptions, paths []string) error {
	outputDir, err := executableOutputDir()
	if err != nil {
		return err
	}

	return runParserInOutputDir(out, warn, options, paths, outputDir)
}

func runParserInOutputDir(out io.Writer, warn io.Writer, options parserOptions, paths []string, outputDir string) error {
	for _, path := range paths {
		logPath := demoLogPath(outputDir, path)
		logFile, err := os.Create(logPath)
		if err != nil {
			return fmt.Errorf("%s: create %s: %w", path, logPath, err)
		}

		demoOut := bufio.NewWriterSize(io.MultiWriter(out, logFile), 64*1024)
		if _, err := fmt.Fprintf(demoOut, "--- START - %s ---\n", path); err != nil {
			_ = logFile.Close()
			return fmt.Errorf("%s: write %s: %w", path, logPath, err)
		}

		parser := newParserWithWarning(demoOut, warn, options)
		if err := parser.parseFile(path); err != nil {
			_ = demoOut.Flush()
			_ = logFile.Close()
			return fmt.Errorf("%s: %w", path, err)
		}

		if _, err := fmt.Fprintf(demoOut, "---  END  - %s ---\n", path); err != nil {
			_ = demoOut.Flush()
			_ = logFile.Close()
			return fmt.Errorf("%s: write %s: %w", path, logPath, err)
		}
		if err := demoOut.Flush(); err != nil {
			_ = logFile.Close()
			return fmt.Errorf("%s: flush %s: %w", path, logPath, err)
		}

		if err := logFile.Close(); err != nil {
			return fmt.Errorf("%s: close %s: %w", path, logPath, err)
		}
	}

	return nil
}

func executableOutputDir() (string, error) {
	executablePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}

	return filepath.Dir(executablePath), nil
}

func demoLogPath(outputDir string, demoPath string) string {
	base := filepath.Base(demoPath)
	ext := filepath.Ext(base)
	if ext != "" {
		base = strings.TrimSuffix(base, ext)
	}

	return filepath.Join(outputDir, "log-"+base+".txt")
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
