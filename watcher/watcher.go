package watcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/fatih/color"
	"github.com/fsnotify/fsnotify"
)

type Config struct {
	GoPaths     []string
	NonGoPaths  []string
	BuildFlags  []string
	RuntimeArgs []string
	Vendor      bool
	PrintFiles  bool

	// Non serialized fields
	Stdout, Stderr io.Writer                `json:"-"`
	OnFileChange   func(file string)        `json:"-"`
	Logf           func(s string, a ...any) `json:"-"`
}

func Run(ctx context.Context, c Config) error {
	if len(c.GoPaths) == 0 {
		c.GoPaths = []string{"."}
	}
	if c.Logf == nil {
		c.Logf = log.Printf
	}
	for _, a := range c.BuildFlags {
		if isOutputFlag(a) {
			return fmt.Errorf("-o build flag is disallowed because gowatch manages the go build for you")
		}
	}
	files, err := getUniqueFiles(ctx, c.GoPaths, c.NonGoPaths, c.Vendor)
	if err != nil {
		return fmt.Errorf("getUniqueFiles: %w", err)
	}
	if c.PrintFiles {
		for _, f := range files {
			fmt.Println(f)
		}
	}
	handler, cleanup, err := getHandler(ctx, c)
	if err != nil {
		return fmt.Errorf("getHandler: %w", err)
	}
	defer cleanup()
	return watch(ctx, files, handler, c.OnFileChange, c.Logf)
}

func getHandler(ctx context.Context, c Config) (handler func() error, cleanup func() error, err error) {
	buildFlags := c.BuildFlags
	runtimeArgs := c.RuntimeArgs
	stdout, stderr := c.Stdout, c.Stderr
	logf := c.Logf

	wd, err := os.Getwd()
	if err != nil {
		return nil, nil, fmt.Errorf("os.Getwd: %w", err)
	}
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
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
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		cmd.Env = os.Environ()
		err = cmd.Start()
		return cmd, err
	}

	var cmd *exec.Cmd
	handler = func() error {
		if cmd != nil {
			err = cmd.Process.Signal(os.Interrupt)
			// TODO: call cmd.Process.Kill() if need be.
			if err != nil {
				return fmt.Errorf("process.Interrupt: %w", err)
			}
			err = cmd.Wait()
			var exitErr *exec.ExitError
			if err != nil && !errors.As(err, &exitErr) {
				logf("error exiting from previous program: %v", err)
			}
		}
		cmd, err = runCmd()
		if err != nil {
			return fmt.Errorf("runCmd: %w", err)
		}
		return nil
	}
	cleanup = func() error {
		if cmd == nil {
			return nil
		}
		return cmd.Wait()
	}
	return handler, cleanup, nil
}

func isOutputFlag(f string) bool {
	return f == "-o" || f == "--o" || strings.HasPrefix(f, "-o=") || strings.HasPrefix(f, "--o=")
}

func watch(
	ctx context.Context,
	files []string,
	handler func() error,
	onFileChange func(string),
	logf func(s string, a ...any),
) error {
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
	go func() {
		if err := handler(); err != nil {
			logf("error building Go program: %v", err)
		}
		for event := range watcher.Events {
			if ctx.Err() != nil {
				return
			}
			if event.Op&fsnotify.Write == fsnotify.Write {
				if onFileChange != nil {
					onFileChange(event.Name)
				}
				logf(color.MagentaString("modified file: %v", event.Name))
				if err := handler(); err != nil {
					logf("error re-building Go program: %v", err)
				}
			}
		}
	}()
	<-ctx.Done()
	return ctx.Err()
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

func getFiles(ctx context.Context, paths []string, vendor, goFiles bool) ([]string, error) {
	files := []string{}
	for _, pathName := range paths {
		if !vendor && pathName == "vendor" {
			continue
		}
		fi, err := os.Stat(pathName)
		if err != nil {
			return nil, fmt.Errorf("error stating %q: %w", pathName, err)
		}
		if !fi.IsDir() {
			if goFiles && filepath.Ext(pathName) != ".go" {
				continue
			}
			files = append(files, pathName)
			continue
		}
		dirFiles, err := os.ReadDir(pathName)
		if err != nil {
			return nil, fmt.Errorf("os.ReadDir(%q): %w", pathName, err)
		}
		for _, df := range dirFiles {
			entryName := df.Name()
			if entryName == ".git" {
				continue
			}
			if df.IsDir() {
				innerDir := filepath.Join(pathName, entryName)
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
			files = append(files, filepath.Join(pathName, df.Name()))
		}
	}
	return files, nil
}
