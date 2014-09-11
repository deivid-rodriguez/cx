package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/cloud66/cloud66"
	"github.com/cloud66/cx/term"

	"github.com/jcoene/honeybadger"
	"github.com/mgutz/ansi"
)

type Command struct {
	Run        func(cmd *Command, args []string)
	Flag       flag.FlagSet
	NeedsStack bool

	Usage    string
	Category string
	Short    string
	Long     string
}

var (
	client     cloud66.Client
	debugMode  bool   = false
	VERSION    string = "dev"
	BUILD_DATE string = ""
	tokenFile  string = "cx.json"
)

func (c *Command) printUsage() {
	c.printUsageTo(os.Stderr)
}

func (c *Command) printUsageTo(w io.Writer) {
	if c.Runnable() {
		fmt.Fprintf(w, "Usage: cx %s\n\n", c.FullUsage())
	}
	fmt.Fprintln(w, strings.Trim(c.Long, "\n"))
}

func (c *Command) FullUsage() string {
	if c.NeedsStack {
		return c.Name() + " [-s <stack>]" + strings.TrimPrefix(c.Usage, c.Name())
	}
	return c.Usage
}

func (c *Command) Name() string {
	name := c.Usage
	i := strings.Index(name, " ")
	if i >= 0 {
		name = name[:i]
	}
	return name
}

func (c *Command) Runnable() bool {
	return c.Run != nil
}

const extra = " (extra)"

func (c *Command) List() bool {
	return c.Short != "" && !strings.HasSuffix(c.Short, extra)
}

func (c *Command) ListAsExtra() bool {
	return c.Short != "" && strings.HasSuffix(c.Short, extra)
}

func (c *Command) ShortExtra() string {
	return c.Short[:len(c.Short)-len(extra)]
}

var commands = []*Command{
	cmdStacks,
	cmdRedeploy,
	cmdOpen,
	cmdSettings,
	cmdSet,
	cmdServerSettings,
	cmdServerSet,
	cmdEnvVars,
	cmdEnvVarsSet,
	cmdLease,
	cmdRestart,
	cmdRun,
	cmdServers,
	cmdSsh,
	cmdTail,
	cmdUpload,
	cmdDownload,
	cmdBackups,
	cmdDownloadBackup,
	cmdClearCaches,

	cmdVersion,
	cmdUpdate,
	cmdHelp,
	cmdInfo,

	helpCommands,
	helpEnviron,
	helpMore,
}

var (
	flagStack       *cloud66.Stack
	flagStackName   string
	flagEnvironment string
)

func main() {
	honeybadger.ApiKey = "09d82034"

	if os.Getenv("CXENVIRONMENT") != "" {
		tokenFile = "cx_" + os.Getenv("CXENVIRONMENT") + ".json"
		fmt.Printf("Running against %s environment\n", os.Getenv("CXENVIRONMENT"))
		honeybadger.Environment = os.Getenv("CXENVIRONMENT")
	} else {
		honeybadger.Environment = "production"
	}

	log.SetFlags(0)

	// make sure command is specified, disallow global args
	args := os.Args[1:]

	if len(args) < 1 || strings.IndexRune(args[0], '-') == 0 {
		printUsageTo(os.Stderr)
		os.Exit(2)
	}

	if args[0] == cmdUpdate.Name() {
		cmdUpdate.Run(cmdUpdate, args[1:])
		return
	} else if VERSION != "dev" {
		defer backgroundRun()
	}

	if !term.IsANSI(os.Stdout) {
		ansi.DisableColors(true)
	}

	// don't need registration if we are only checking the version
	if args[0] != "version" {
		initClients()
	}

	for _, cmd := range commands {

		if cmd.Name() == args[0] && cmd.Run != nil {
			defer recoverPanic()

			cmd.Flag.Usage = func() {
				cmd.printUsage()
			}
			if cmd.NeedsStack {
				cmd.Flag.StringVar(&flagStackName, "s", "", "stack name")
				cmd.Flag.StringVar(&flagEnvironment, "e", "", "stack environment")
			}
			if err := cmd.Flag.Parse(args[1:]); err != nil {
				os.Exit(2)
			}
			if cmd.NeedsStack {
				// by default print server output to stdout
				var toSdout bool = true

				// when command is 'run', do not print server output to stdout
				if args[0] == "run" {
					toSdout = false
				}

				s, err := stack(toSdout)
				switch {
				case err == nil && s == nil:
					msg := "no stack specified"
					if err != nil {
						msg = err.Error()
					}
					printError(msg)
					cmd.printUsage()
					os.Exit(2)
				case err != nil:
					printFatal(err.Error())
				}
			}
			cmd.Run(cmd, cmd.Flag.Args())
			return
		}
	}

	// invalid command
	fmt.Fprintf(os.Stderr, "Unknown command: %s\n", args[0])
	if g := suggest(args[0]); len(g) > 0 {
		fmt.Fprintf(os.Stderr, "Possible alternatives: %v\n", strings.Join(g, " "))
	}
	fmt.Fprintf(os.Stderr, "Run 'cx help' for usage.\n")
	os.Exit(2)
}

func initClients() {
	// is there a token file?
	_, err := os.Stat(filepath.Join(cxHome(), tokenFile))
	if err != nil {
		fmt.Println("No previous authentication found.")
		cloud66.Authorize(cxHome(), tokenFile)
		os.Exit(1)
	} else {
		client = cloud66.GetClient(cxHome(), tokenFile, VERSION)
		debugMode = os.Getenv("CXDEBUG") != ""
		client.Debug = debugMode
	}
}

func recoverPanic() {
	if VERSION != "dev" {
		if rec := recover(); rec != nil {
			report, err := honeybadger.NewReport(rec)
			if err != nil {
				printError("reporting crash failed: %s", err.Error())
				panic(rec)
			}
			report.AddContext("Version", VERSION)
			report.AddContext("Platform", runtime.GOOS)
			report.AddContext("Architecture", runtime.GOARCH)
			report.AddContext("DebugMode", debugMode)
			result := report.Send()
			if result != nil {
				printError("reporting crash failed: %s", result.Error())
				panic(rec)
			}
			printFatal("cx encountered and reported an internal client error")
		}
	}
}

func filterByEnvironment(item interface{}) bool {
	if flagEnvironment == "" {
		return true
	}

	return strings.HasPrefix(strings.ToLower(item.(cloud66.Stack).Environment), strings.ToLower(flagEnvironment))
}

func stack(toSdout ...bool) (*cloud66.Stack, error) {
	if flagStack != nil {
		return flagStack, nil
	}

	var err error
	if flagStackName != "" {
		stacks, err := client.StackListWithFilter(filterByEnvironment)
		if err != nil {
			return nil, err
		}
		var stackNames []string
		for _, stack := range stacks {
			stackNames = append(stackNames, stack.Name)
		}
		idx, err := fuzzyFind(stackNames, flagStackName, false)
		if err != nil {
			return nil, err
		}

		flagStack = &stacks[idx]

		// toSdout is of type []bool. Take first value
		if len(toSdout) == 0 || toSdout[0] == true {
			fmt.Printf("Stack: %s ", flagStack.Name)
			if flagEnvironment != "" {
				fmt.Printf("(%s)\n", flagStack.Environment)
			} else {
				fmt.Println("")
			}
		}

		return flagStack, err
	}

	if stack := os.Getenv("CXSTACK"); stack != "" {
		// the environment variable should be exact match
		flagStack, err = client.StackInfo(stack)
		return flagStack, err
	}

	return stackFromGitRemote(remoteGitUrl(), localGitBranch())
}

func mustStack() *cloud66.Stack {
	stack, err := stack()
	if err != nil {
		printFatal(err.Error())
	}
	return stack
}
