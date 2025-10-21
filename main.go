package main

import (
	"log"
	"os"

	"github.com/cwbudde/mayflycirclefit/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		log.Fatalf("Error: %v\n", err)
		os.Exit(1)
	}
}
