package watcher

import (
	"context"
	"fmt"
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
}

func Run(ctx context.Context, c Config) error {
	if len(c.GoPaths) == 0 {
		c.GoPaths = []string{"."}
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
		err := cmd.Process.Signal(os.Interrupt)
		// TODO: call cmd.Process.Kill() if need be.
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
					return
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
