package cobra

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// PositionalArgs validates positional arguments after flag parsing.
type PositionalArgs func(*Command, []string) error

// Command is a minimal Cobra-compatible command implementation for this tool.
type Command struct {
	Use          string
	Short        string
	SilenceUsage bool
	Args         PositionalArgs
	RunE         func(*Command, []string) error

	flags FlagSet
	out   io.Writer
	err   io.Writer
	args  []string
}

// Flag matches the subset of Cobra flag metadata used by the parser CLI.
type Flag struct {
	Name        string
	Usage       string
	IntValue    *int
	StringValue *string
	NoOptDefVal string
}

// FlagSet stores supported flags in declaration order for stable usage output.
type FlagSet struct {
	order  []*Flag
	byName map[string]*Flag
}

// MinimumNArgs validates that at least n positional arguments were provided.
func MinimumNArgs(n int) PositionalArgs {
	return func(cmd *Command, args []string) error {
		if len(args) < n {
			return fmt.Errorf("requires at least %d arg(s), only received %d", n, len(args))
		}
		return nil
	}
}

// SetOut overrides the command stdout stream.
func (c *Command) SetOut(writer io.Writer) {
	c.out = writer
}

// SetErr overrides the command stderr stream.
func (c *Command) SetErr(writer io.Writer) {
	c.err = writer
}

// SetArgs overrides argv for tests.
func (c *Command) SetArgs(args []string) {
	c.args = append([]string(nil), args...)
}

// Flags returns the mutable flag set for the command.
func (c *Command) Flags() *FlagSet {
	return &c.flags
}

// OutOrStdout returns the configured stdout stream.
func (c *Command) OutOrStdout() io.Writer {
	if c.out != nil {
		return c.out
	}
	return os.Stdout
}

// ErrOrStderr returns the configured stderr stream.
func (c *Command) ErrOrStderr() io.Writer {
	if c.err != nil {
		return c.err
	}
	return os.Stderr
}

// Execute parses flags, validates args, and runs the command callback.
func (c *Command) Execute() error {
	args := os.Args[1:]
	if c.args != nil {
		args = append([]string(nil), c.args...)
	}

	positionals, showHelp, err := c.flags.parse(args)
	if showHelp {
		_, _ = io.WriteString(c.OutOrStdout(), c.UsageString())
		return nil
	}
	if err != nil {
		if !c.SilenceUsage {
			_, _ = io.WriteString(c.ErrOrStderr(), c.UsageString())
		}
		return err
	}

	if c.Args != nil {
		if err := c.Args(c, positionals); err != nil {
			if !c.SilenceUsage {
				_, _ = io.WriteString(c.ErrOrStderr(), c.UsageString())
			}
			return err
		}
	}

	if c.RunE != nil {
		if err := c.RunE(c, positionals); err != nil {
			if !c.SilenceUsage {
				_, _ = io.WriteString(c.ErrOrStderr(), c.UsageString())
			}
			return err
		}
	}

	return nil
}

// UsageString formats a compact Cobra-like help message.
func (c *Command) UsageString() string {
	var builder strings.Builder

	if c.Short != "" {
		builder.WriteString(c.Short)
		builder.WriteString("\n\n")
	}

	builder.WriteString("Usage:\n")
	builder.WriteString("  ")
	builder.WriteString(c.Use)
	builder.WriteString("\n")

	if len(c.flags.order) > 0 {
		builder.WriteString("\nFlags:\n")
		for _, flag := range c.flags.order {
			builder.WriteString("      --")
			builder.WriteString(flag.Name)
			switch {
			case flag.IntValue != nil && flag.NoOptDefVal != "":
				builder.WriteString("[=int]")
			case flag.IntValue != nil:
				builder.WriteString(" int")
			case flag.StringValue != nil:
				builder.WriteString(" string")
			}
			padding := 24 - len(flag.Name)
			if padding < 2 {
				padding = 2
			}
			builder.WriteString(strings.Repeat(" ", padding))
			builder.WriteString(flag.Usage)
			builder.WriteString("\n")
		}
	}

	builder.WriteString("  -h, --help               help for ")
	builder.WriteString(c.commandName())
	builder.WriteString("\n")

	return builder.String()
}

func (c *Command) commandName() string {
	fields := strings.Fields(c.Use)
	if len(fields) == 0 {
		return "command"
	}
	return fields[0]
}

// IntVar registers an integer flag.
func (f *FlagSet) IntVar(target *int, name string, value int, usage string) {
	f.ensure()
	*target = value

	flag := &Flag{
		Name:     name,
		Usage:    usage,
		IntValue: target,
	}

	f.order = append(f.order, flag)
	f.byName[name] = flag
}

// StringVar registers a string flag.
func (f *FlagSet) StringVar(target *string, name string, value string, usage string) {
	f.ensure()
	*target = value

	flag := &Flag{
		Name:        name,
		Usage:       usage,
		StringValue: target,
	}

	f.order = append(f.order, flag)
	f.byName[name] = flag
}

// Lookup returns the registered flag by name.
func (f *FlagSet) Lookup(name string) *Flag {
	if f.byName == nil {
		return nil
	}
	return f.byName[name]
}

func (f *FlagSet) ensure() {
	if f.byName == nil {
		f.byName = make(map[string]*Flag)
	}
}

func (f *FlagSet) parse(args []string) ([]string, bool, error) {
	positionals := make([]string, 0, len(args))

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			positionals = append(positionals, args[i+1:]...)
			return positionals, false, nil
		case arg == "-h" || arg == "--help":
			return positionals, true, nil
		case !strings.HasPrefix(arg, "-") || arg == "-":
			positionals = append(positionals, arg)
		case strings.HasPrefix(arg, "--"):
			nameValue := strings.TrimPrefix(arg, "--")
			name, value, hasValue := strings.Cut(nameValue, "=")
			flag := f.Lookup(name)
			if flag == nil {
				return nil, false, fmt.Errorf("unknown flag: --%s", name)
			}

			if !hasValue {
				nextValue, consumed, err := f.nextValue(flag, args, i+1)
				if err != nil {
					return nil, false, err
				}
				if consumed {
					value = nextValue
					i++
				} else if flag.NoOptDefVal != "" {
					value = flag.NoOptDefVal
				} else {
					return nil, false, fmt.Errorf("flag needs an argument: --%s", name)
				}
			}

			switch {
			case flag.IntValue != nil:
				parsed, err := strconv.Atoi(value)
				if err != nil {
					return nil, false, fmt.Errorf("invalid argument %q for --%s: %w", value, name, err)
				}
				*flag.IntValue = parsed
			case flag.StringValue != nil:
				*flag.StringValue = value
			default:
				return nil, false, fmt.Errorf("unsupported flag type for --%s", name)
			}
		default:
			return nil, false, fmt.Errorf("unknown shorthand flag: %s", arg)
		}
	}

	return positionals, false, nil
}

// optionalIntValue treats the following argv token as a flag value only when
// it is a valid integer, which allows --flag to fall back to NoOptDefVal.
func (f *FlagSet) optionalIntValue(args []string, index int) (string, bool) {
	if index >= len(args) {
		return "", false
	}
	if _, err := strconv.Atoi(args[index]); err != nil {
		return "", false
	}
	return args[index], true
}

func (f *FlagSet) nextValue(flag *Flag, args []string, index int) (string, bool, error) {
	if flag.IntValue != nil && flag.NoOptDefVal != "" {
		value, ok := f.optionalIntValue(args, index)
		return value, ok, nil
	}
	if index >= len(args) {
		return "", false, nil
	}
	return args[index], true, nil
}
