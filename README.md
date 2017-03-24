# gowatch
A simple `.go` file watcher that will stop & restart `main.go` on file changes.

## Motivation

I just want to stop my server, and run it again every time I save a file.

## installation

`go get github.com/marwan-at-work/gowatch`

## Usage
from your main app directory, run `gowatch`

## What it does

it reads your current working directory and runs the two typical commands:

- `go build main.go`

- `./main`

So this only works in `main` packages and you have to have a `main.go`

Also, this ignores your `vendor` folder & your `_test.go` files.

#### FAQ

Q: Why doesn't it just run `go run main.go`?

A: `go run` does the same thing as `go build`, but calls the resulting binary as a subprocess. This makes it harder to reach `stdout` and killing the `main.go` process, won't necessarily kill the subprocess, so you end up trying to run the server twice.

Q: But there's a bunch of those file watchers out there already.

A: Trust me, I didn't want to build this. But some tools don't give you logs, some tools don't gracefully shutdown the server.

Q: Will it work for me?

A: It should, #famouslastwords. Hit up the issues if it doesn't. I'd love to make it more robust.
