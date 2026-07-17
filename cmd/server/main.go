package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"jokism-content/internal/app"
)

func main() {
	a, err := app.New()
	if err != nil { log.Fatal(err) }
	ctx, stop := signal.NotifyContext(a.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := a.Run(ctx); err != nil { log.Fatal(err) }
}
