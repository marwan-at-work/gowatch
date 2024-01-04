package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"

	"github.com/urfave/cli/v2"
	"marwan.io/gowatch/watcher"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, os.Kill)
	defer cancel()
	app := &cli.App{
		Name:  "gowatch",
		Usage: "Automatically restart Go processes on file changes",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "cwd",
				Usage: "set the current working directoy for the Go process",
			},
			&cli.StringSliceFlag{
				Name:  "additiona-files",
				Usage: "Comma separated directories or files to watch",
			},
			&cli.BoolFlag{
				Name:  "vendor",
				Usage: "Also watch the vendor directory",
			},
			&cli.StringSliceFlag{
				Name:  "build-flags",
				Usage: "flags to send to the 'go build'",
			},
			&cli.BoolFlag{
				Name:    "print-files",
				Aliases: []string{"p"},
				Usage:   "print all watched files",
			},
		},
		Commands: []*cli.Command{
			{
				Name:  "init",
				Usage: "creates a gowatch.json file in the current working directory",
				Action: func(*cli.Context) error {
					f, err := os.Create("gowatch.json")
					if err != nil {
						return err
					}
					defer f.Close()
					enc := json.NewEncoder(f)
					enc.SetIndent("", "\t")
					return enc.Encode(watcher.Config{})
				},
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
	var c watcher.Config
	err = json.NewDecoder(f).Decode(&c)
	if err != nil {
		return fmt.Errorf("json.Decode: %w", err)
	}
	return watcher.Run(ctx, c)
}

func runCLI(c *cli.Context) error {
	cfg := watcher.Config{
		Dir:             c.String("cwd"),
		AdditionalFiles: c.StringSlice("additional-files"),
		BuildFlags:      c.StringSlice("build-flag"),
		RuntimeArgs:     c.Args().Slice(),
		Vendor:          c.Bool("vendor"),
		PrintFiles:      c.Bool("print-files"),
	}
	return watcher.Run(c.Context, cfg)
}
