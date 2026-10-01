package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Someblueman/airc/internal/server"
)

func main() {
	flags := flag.NewFlagSet("aircd", flag.ExitOnError)
	listen := flags.String("listen", "127.0.0.1:6667", "TCP listen address (loopback by default)")
	unixPath := flags.String("unix", "", "Unix domain socket path")
	history := flags.Int("history", 0, "number of recent messages to retain in memory")
	historyFile := flags.String("history-file", "", "append messages to this JSON-lines file and reload them at startup (requires --history)")
	topicsFile := flags.String("topics-file", "", "save channel headers (topics) to this JSON file and reload them at startup (default: next to --history-file)")
	maxConnections := flags.Int("max-connections", 128, "maximum simultaneous clients")
	maxMessage := flags.Int("max-message-size", 4096, "maximum message body size in bytes (1-4096)")
	logFormat := flags.String("log-format", "text", "log format: text or json")
	if err := flags.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	if *logFormat != "text" && *logFormat != "json" {
		fmt.Fprintln(os.Stderr, "--log-format must be text or json")
		os.Exit(2)
	}
	if *maxMessage < 1 || *maxMessage > 4096 {
		fmt.Fprintln(os.Stderr, "--max-message-size must be between 1 and 4096")
		os.Exit(2)
	}
	if *maxConnections < 1 || *maxConnections > 1024 {
		fmt.Fprintln(os.Stderr, "--max-connections must be between 1 and 1024")
		os.Exit(2)
	}
	if *history < 0 || *history > 10000 {
		fmt.Fprintln(os.Stderr, "--history must be between 0 and 10000")
		os.Exit(2)
	}
	var handler slog.Handler
	if *logFormat == "json" {
		handler = slog.NewJSONHandler(os.Stderr, nil)
	} else {
		handler = slog.NewTextHandler(os.Stderr, nil)
	}
	logger := slog.New(handler)
	srv := server.New(server.Config{HistoryLimit: *history, MaxConnections: *maxConnections, MaxMessageSize: *maxMessage, Logger: logger})
	if *historyFile != "" {
		if *history == 0 {
			fmt.Fprintln(os.Stderr, "--history-file requires --history N")
			os.Exit(2)
		}
		if err := srv.RestoreHistory(*historyFile); err != nil {
			logger.Error("history_restore_failed", "error", err.Error())
			os.Exit(1)
		}
	}
	if *topicsFile == "" && *historyFile != "" {
		*topicsFile = *historyFile + ".topics.json"
	}
	if *topicsFile != "" {
		if err := srv.RestoreTopics(*topicsFile); err != nil {
			logger.Error("topics_restore_failed", "error", err.Error())
			os.Exit(1)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := srv.Run(ctx, *listen, *unixPath); err != nil {
		logger.Error("server_stopped", "error", err.Error())
		os.Exit(1)
	}
}
