<img src="https://user-images.githubusercontent.com/16294261/204098353-e8042b8c-3f3c-43e4-9974-86274abb634b.png" width="400" />

A simple Go file watcher that will stop & restart your `main()` function on file changes.

## Motivation

I just want to stop my server, and run it again every time I save a file.

## installation

`go install marwan.io/gowatch@latest`

## Usage
from your main app directory, run `gowatch`

## What it does

it reads your current working directory and runs the two typical commands:

- `go build`

- `./<name-of-compiled-binary>`

So this only works in `main` packages.

Also, this ignores your `vendor` folder & your `_test.go` files.

#### FAQ

Q: Why doesn't it just run `go run main.go`?

A: `go run` does the same thing as `go build`, but calls the resulting binary as a subprocess. This makes it harder to reach `stdout` and killing the `main.go` process, won't necessarily kill the subprocess, so you end up trying to run the server twice (which ends up with "port is already taken" kind of error).

Q: But there's a bunch of those file watchers out there already.

A: Trust me, I didn't want to build this. But some tools don't give you logs, some tools don't gracefully shutdown the server.

Q: Will it work for me?

A: It should, #famouslastwords. Hit up the issues if it doesn't. I'd love to make it more robust.
