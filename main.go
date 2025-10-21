package main

import (
	"log"
	"os"
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		log.Fatalf("Error: %v\n", err)
		os.Exit(1)
	}
}
