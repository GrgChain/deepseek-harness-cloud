package main

import (
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/bootstrap"
	"log/slog"
	"os"
)

func main() {
	if err := bootstrap.Run("api"); err != nil {
		slog.Error("API stopped", "error", err)
		os.Exit(1)
	}
}
