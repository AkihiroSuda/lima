package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/lima-vm/lima/pkg/debugutil"
	"github.com/lima-vm/lima/pkg/fsutil"
	"github.com/lima-vm/lima/pkg/osutil"
	"github.com/lima-vm/lima/pkg/store/dirnames"
	"github.com/lima-vm/lima/pkg/version"
	"github.com/mattn/go-isatty"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

const (
	DefaultInstanceName = "default"
	basicCommand        = "basic"
	advancedCommand     = "advanced"
)

func main() {
	if err := newApp().Execute(); err != nil {
		handleExitCoder(err)
		logrus.Fatal(err)
	}
}

func newApp() *cobra.Command {
	templatesDir := "$PREFIX/share/lima/templates"
	if exe, err := os.Executable(); err == nil {
		binDir := filepath.Dir(exe)
		prefixDir := filepath.Dir(binDir)
		templatesDir = filepath.Join(prefixDir, "share/lima/templates")
	}

	rootCmd := &cobra.Command{
		Use:     "limactl",
		Short:   "Lima: Linux virtual machines",
		Version: strings.TrimPrefix(version.Version, "v"),
		Example: fmt.Sprintf(`  Start the default instance:
  $ limactl start

  Open a shell:
  $ lima

  Run a container:
  $ lima nerdctl run -d --name nginx -p 8080:80 nginx:alpine

  Stop the default instance:
  $ limactl stop

  See also template YAMLs: %s`, templatesDir),
		SilenceUsage:      true,
		SilenceErrors:     true,
		DisableAutoGenTag: true,
	}
	rootCmd.PersistentFlags().String("log-level", "", "Set the logging level [trace, debug, info, warn, error]")
	rootCmd.PersistentFlags().String("log-format", "text", "Set the logging format [text, json]")
	rootCmd.PersistentFlags().Bool("debug", false, "debug mode")
	// TODO: "survey" does not support using cygwin terminal on windows yet
	rootCmd.PersistentFlags().Bool("tty", isatty.IsTerminal(os.Stdout.Fd()), "Enable TUI interactions such as opening an editor. Defaults to true when stdout is a terminal. Set to false for automation.")
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		l, _ := cmd.Flags().GetString("log-level")
		if l != "" {
			lvl, err := logrus.ParseLevel(l)
			if err != nil {
				return err
			}
			logrus.SetLevel(lvl)
		}

		logFormat, _ := cmd.Flags().GetString("log-format")
		switch logFormat {
		case "json":
			formatter := new(logrus.JSONFormatter)
			logrus.StandardLogger().SetFormatter(formatter)
		case "text":
			// logrus use text format by default.
			if runtime.GOOS == "windows" && isatty.IsCygwinTerminal(os.Stderr.Fd()) {
				formatter := new(logrus.TextFormatter)
				// the default setting does not recognize cygwin on windows
				formatter.ForceColors = true
				logrus.StandardLogger().SetFormatter(formatter)
			}
		default:
			return fmt.Errorf("unsupported log-format: %q", logFormat)
		}

		debug, _ := cmd.Flags().GetBool("debug")
		if debug {
			logrus.SetLevel(logrus.DebugLevel)
			debugutil.Debug = true
		}

		if osutil.IsBeingRosettaTranslated() && cmd.Parent().Name() != "completion" && cmd.Name() != "generate-doc" && cmd.Name() != "validate" {
			// running under rosetta would provide inappropriate runtime.GOARCH info, see: https://github.com/lima-vm/lima/issues/543
			// allow commands that are used for packaging to run under rosetta to allow cross-architecture builds
			return errors.New("limactl is running under rosetta, please reinstall lima with native arch")
		}

		if os.Geteuid() == 0 && cmd.Name() != "generate-doc" {
			return errors.New("must not run as the root user")
		}
		// Make sure either $HOME or $LIMA_HOME is defined, so we don't need
		// to check for errors later
		dir, err := dirnames.LimaDir()
		if err != nil {
			return err
		}
		// Make sure that directory is on a local filesystem, not on NFS
		// if the directory does not yet exist, check the home directory
		_, err = os.Stat(dir)
		if errors.Is(err, os.ErrNotExist) {
			dir = filepath.Dir(dir)
		}
		nfs, err := fsutil.IsNFS(dir)
		if err != nil {
			return err
		}
		if nfs {
			return errors.New("must not run on NFS dir")
		}
		return nil
	}
	rootCmd.AddGroup(&cobra.Group{ID: "basic", Title: "Basic Commands:"})
	rootCmd.AddGroup(&cobra.Group{ID: "advanced", Title: "Advanced Commands:"})
	rootCmd.AddCommand(
		newCreateCommand(),
		newStartCommand(),
		newStopCommand(),
		newShellCommand(),
		newCopyCommand(),
		newListCommand(),
		newDeleteCommand(),
		newValidateCommand(),
		newSudoersCommand(),
		newPruneCommand(),
		newHostagentCommand(),
		newGuestInstallCommand(),
		newInfoCommand(),
		newShowSSHCommand(),
		newDebugCommand(),
		newEditCommand(),
		newFactoryResetCommand(),
		newDiskCommand(),
		newUsernetCommand(),
		newGenDocCommand(),
		newGenSchemaCommand(),
		newSnapshotCommand(),
		newProtectCommand(),
		newUnprotectCommand(),
		newTunnelCommand(),
		newTemplateCommand(),
	)
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		rootCmd.AddCommand(startAtLoginCommand())
	}

	return rootCmd
}

type ExitCoder interface {
	error
	ExitCode() int
}

func handleExitCoder(err error) {
	if err == nil {
		return
	}

	if exitErr, ok := err.(ExitCoder); ok {
		os.Exit(exitErr.ExitCode()) //nolint:revive // it's intentional to call os.Exit in this function
		return
	}
}

// WrapArgsError annotates cobra args error with some context, so the error message is more user-friendly.
func WrapArgsError(argFn cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		err := argFn(cmd, args)
		if err == nil {
			return nil
		}

		return fmt.Errorf("%q %s.\nSee '%s --help'.\n\nUsage:  %s\n\n%s",
			cmd.CommandPath(), err.Error(),
			cmd.CommandPath(),
			cmd.UseLine(), cmd.Short,
		)
	}
}
