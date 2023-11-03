<img src="https://user-images.githubusercontent.com/16294261/204098353-e8042b8c-3f3c-43e4-9974-86274abb634b.png" width="200" />

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

Q: How does it compare with other tools?

A: I haven't tried most of them, but I wanted to make this as simple as just running one command to get what I'm looking for without having to turn and twist a lot of knobs. 

