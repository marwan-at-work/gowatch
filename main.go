package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/fatih/color"
	"github.com/fsnotify/fsnotify"
	"github.com/urfave/cli/v2"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, os.Kill)
	defer cancel()
	app := &cli.App{
		Name:  "gowatch",
		Usage: "Automatically restart Go processes on file changes",
		Flags: []cli.Flag{
			&cli.StringSliceFlag{
				Name:  "go-dir",
				Usage: "Comma separated directories to watch Go files",
			},
			&cli.StringSliceFlag{
				Name:  "nongo-dir",
				Usage: "Comma separated directories to watch all files",
			},
			&cli.BoolFlag{
				Name:    "include-vendor",
				Aliases: []string{"vendor", "v"},
				Usage:   "Also watch the vendor directory",
			},
			&cli.StringSliceFlag{
				Name:  "build-flag",
				Usage: "flags to send to the 'go build'",
			},
			&cli.BoolFlag{
				Name:    "print-files",
				Aliases: []string{"p"},
				Usage:   "print all watched files",
			},
		},
		Action: run,
	}
	err := app.RunContext(ctx, os.Args)
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}

const configFile = "gowatch.json"

func run(c *cli.Context) error {
	if _, err := os.Stat(configFile); err == nil {
		return runFile(c.Context)
	}
	return runCLI(c)
}

func runFile(ctx context.Context) error {
	f, err := os.Open(configFile)
	if err != nil {
		return fmt.Errorf("configFile: %w", err)
	}
	defer f.Close()
	var c config
	err = json.NewDecoder(f).Decode(&c)
	if err != nil {
		return fmt.Errorf("json.Decode: %w", err)
	}
	return runWatcher(ctx, c)
}

func runCLI(c *cli.Context) error {
	cfg := config{
		GoDirs:      c.StringSlice("go-dir"),
		NonGoDirs:   c.StringSlice("nongo-dir"),
		BuildFlags:  c.StringSlice("build-flag"),
		RuntimeArgs: c.Args().Slice(),
		Vendor:      c.Bool("include-vendor"),
		PrintFiles:  c.Bool("print-files"),
	}
	return runWatcher(c.Context, cfg)
}

type config struct {
	GoDirs      []string
	NonGoDirs   []string
	BuildFlags  []string
	RuntimeArgs []string
	Vendor      bool
	PrintFiles  bool
}

func runWatcher(ctx context.Context, c config) error {
	if len(c.GoDirs) == 0 {
		c.GoDirs = []string{"."}
	}
	for _, a := range c.BuildFlags {
		if isOutputFlag(a) {
			return fmt.Errorf("-o build flag is disallowed because gowatch manages the go build for you")
		}
	}
	files, err := getUniqueFiles(ctx, c.GoDirs, c.NonGoDirs, c.Vendor)
	if err != nil {
		return fmt.Errorf("getUniqueFiles: %w", err)
	}
	if c.PrintFiles {
		for _, f := range files {
			fmt.Println(f)
		}
	}
	handler, err := getHandler(ctx, c.BuildFlags, c.RuntimeArgs)
	if err != nil {
		return fmt.Errorf("getHandler: %w", err)
	}
	return watch(ctx, files, handler)
}

func getHandler(ctx context.Context, buildFlags, runtimeArgs []string) (func() error, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("os.Getwd: %w", err)
	}
	runCmd := func() (*exec.Cmd, error) {
		args := append([]string{"build", "-o=__gowatch"}, buildFlags...)
		cmd := exec.CommandContext(ctx, "go", args...)
		cmd.Dir = wd // TODO: customizable
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err := cmd.Run()
		if err != nil {
			return nil, fmt.Errorf("goBuild: %w", err)
		}

		cmd = exec.CommandContext(ctx, "./__gowatch", runtimeArgs...)
		cmd.Dir = wd
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Env = os.Environ()
		err = cmd.Start()
		return cmd, err
	}
	cmd, err := runCmd()
	if err != nil {
		return nil, fmt.Errorf("runCmd: %w", err)
	}
	return func() error {
		err := cmd.Process.Kill()
		if err != nil {
			return fmt.Errorf("process.Kill: %w", err)
		}
		cmd, err = runCmd()
		if err != nil {
			return fmt.Errorf("runCmd: %w", err)
		}
		return nil
	}, nil
}

func isOutputFlag(f string) bool {
	return f == "-o" || f == "--o" || strings.HasPrefix(f, "-o=") || strings.HasPrefix(f, "--o=")
}

func watch(ctx context.Context, files []string, handler func() error) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("fsnotify.NewWatcher: %w", err)
	}
	defer watcher.Close()
	for _, f := range files {
		err = watcher.Add(f)
		if err != nil {
			return fmt.Errorf("watcher.Add(%q): %w", f, err)
		}
	}
	errCh := make(chan error, 1)
	go func() {
		for event := range watcher.Events {
			if event.Op&fsnotify.Write == fsnotify.Write {
				log.Println(color.MagentaString("modified file: %v", event.Name))
				err := handler()
				if err != nil {
					errCh <- err
				}
			}
		}
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

func getUniqueFiles(ctx context.Context, goDirs, nonGoDirs []string, vendor bool) ([]string, error) {
	files, err := getFiles(ctx, goDirs, vendor, true)
	if err != nil {
		return nil, fmt.Errorf("getGoFiles: %w", err)
	}
	nonGoFiles, err := getFiles(ctx, nonGoDirs, vendor, false)
	if err != nil {
		return nil, fmt.Errorf("getNonGoFiles: %w", err)
	}
	return uniq(files, nonGoFiles), nil
}

func uniq(slices ...[]string) []string {
	mp := map[string]struct{}{}
	final := []string{}
	for _, slc := range slices {
		for _, el := range slc {
			if _, ok := mp[el]; ok {
				continue
			}
			mp[el] = struct{}{}
			final = append(final, el)
		}
	}
	return final
}

func getFiles(ctx context.Context, goDirs []string, vendor, goFiles bool) ([]string, error) {
	files := []string{}
	for _, dir := range goDirs {
		if !vendor && dir == "vendor" {
			continue
		}
		dirFiles, err := os.ReadDir(dir)
		if err != nil {
			return nil, fmt.Errorf("os.ReadDir(%q): %w", dir, err)
		}
		for _, df := range dirFiles {
			entryName := df.Name()
			if entryName == ".git" {
				continue
			}
			if df.IsDir() {
				innerDir := filepath.Join(dir, entryName)
				dirFiles, err := getFiles(ctx, []string{innerDir}, vendor, goFiles)
				if err != nil {
					return nil, fmt.Errorf("getGoFiles(%q): %w", entryName, err)
				}
				files = append(files, dirFiles...)
				continue
			}
			if goFiles && filepath.Ext(entryName) != ".go" {
				continue
			}
			files = append(files, filepath.Join(dir, df.Name()))
		}
	}
	return files, nil
}
