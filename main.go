package main

import (
	"github.com/filebrowser/filebrowser/v2/cmd"
	"runtime"
)

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	cmd.Execute()
}
