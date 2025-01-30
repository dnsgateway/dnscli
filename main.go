package main

import (
	"fmt"
	"os"

	"github.com/sergelogvinov/dnscli/internal/tui"
)

func main() {
	startApp()
}

func startApp() {
	var exitStatus = 1

	t := tui.NewTUI()
	if err := t.Start(); err != nil {
		fmt.Printf("failed to start: %v\n", err)
		os.Exit(exitStatus)
	}
}
