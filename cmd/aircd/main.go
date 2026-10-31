package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/Someblueman/airc/internal/admin"
	"github.com/Someblueman/airc/internal/atomicfile"
	"github.com/Someblueman/airc/internal/bytesize"
	"github.com/Someblueman/airc/internal/pathcheck"
	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/internal/retention"
	"github.com/Someblueman/airc/internal/server"
)

func main() {
	flags := flag.NewFlagSet("aircd", flag.ExitOnError)
	listen := flags.String("listen", "127.0.0.1:6667", "TCP listen address (loopback by default)")
	tlsCert := flags.String("tls-cert", "", "TLS certificate chain PEM (requires --tls-key)")
	tlsKey := flags.String("tls-key", "", "owner-only TLS private key PEM")
	accessFile := flags.String("access-token-file", "", "require this owner-only connection credential before registration")
	gatewayFile := flags.String("gateway-token-file", "", "let a gateway presenting this owner-only credential assert its clients' real sources (WEBIRC)")
	logFile := flags.String("log-file", "", "bounded log file (5 MiB plus one backup)")
	unixPath := flags.String("unix", "", "Unix domain socket path")
	history := flags.Int("history", 0, "maximum recent messages to retain in memory (0 disables history; at most 10000000)")
	historyMemory := flags.String("history-memory", "1GiB", "approximate memory the retained messages may use; the oldest are dropped first")
	historyRetain := flags.String("history-retain", retention.DefaultAge, "how long the history file's archive keeps messages, such as 30d or 72h; 0 keeps them until --history-retain-size")
	historyRetainSize := flags.String("history-retain-size", retention.DefaultSize, "disk the history file and its archive may use; the oldest messages are deleted first; 0 means no limit")
	stateDir := flags.String("state-dir", "", "keep the history file and every state file in this directory under fixed names (history.jsonl, accounts.json, chat.json, handoffs.jsonl, decisions.jsonl, wakes.json, topics.json, profiles.json, moderation.json, pastes.json and pastes/); state files still at their older names beside history.jsonl are used as they are")
	pasteMax := flags.String("paste-max-size", "64KiB", "largest paste a client may upload (1KiB-8MiB)")
	pasteTotal := flags.String("paste-max-total", "64MiB", "disk every paste together may use; the oldest unreferenced go first")
	pasteRetain := flags.String("paste-retain", "7d", "how long a paste is kept after it was last shared or read, such as 7d or 72h; a paste a retained message shares is kept while that message is; 0 keeps pastes until --paste-max-total")
	historyFile := flags.String("history-file", "", "append messages to this JSON-lines file, an archive on disk, and reload the newest at startup (--history defaults to 10000000, so --history-memory bounds the window; default with --state-dir: history.jsonl in it; without --state-dir the other state files sit beside it as PATH.accounts.json, PATH.topics.json and so on)")
	adminTokenFile := flags.String("admin-token-file", os.Getenv("AIRC_ADMIN_TOKEN_FILE"), "enable authenticated administration using this owner-only token file; moderation rules persist in moderation.json under --state-dir, else beside the history file or this file")
	wakeAllow := flags.String("wake-allow", "", "comma-separated URL prefixes accounts may register for wake notices, such as https://ntfy.sh/ (requires an accounts file; default: wake notices off)")
	maxConnections := flags.Int("max-connections", 4096, "maximum simultaneous clients (1-16384)")
	maxMessage := flags.Int("max-message-size", 4096, "maximum message body size in bytes (1-4096)")
	outboundBytes := flags.Int("outbound-bytes", 2<<20, "maximum queued and in-flight bytes per connection (1-16777216)")
	logFormat := flags.String("log-format", "text", "log format: text or json")
	carrierList := flags.String("carriers", "", "advertise these ordered client carriers in 005 CARRIERS and STATUS, e.g. irc:6667,http:6680 (set by airc service install)")
	syncMode := flags.String("sync", "full", "durability of history and state writes: full (survives power loss), fsync (survives an OS crash; much faster on macOS) or none (survives a daemon crash)")
	if err := flags.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	durability, err := atomicfile.ParseSync(*syncMode)
	if err != nil {
		fmt.Fprintln(os.Stderr, "--sync:", err)
		os.Exit(2)
	}
	carriers, err := protocol.ParseCarriers(*carrierList)
	if err != nil {
		fmt.Fprintln(os.Stderr, "--carriers:", err)
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
	if *maxConnections < 1 || *maxConnections > 16384 {
		fmt.Fprintln(os.Stderr, "--max-connections must be between 1 and 16384")
		os.Exit(2)
	}
	if *outboundBytes < 1 || *outboundBytes > 16<<20 {
		fmt.Fprintln(os.Stderr, "--outbound-bytes must be between 1 and 16777216")
		os.Exit(2)
	}
	if *history < 0 || *history > 10_000_000 {
		fmt.Fprintln(os.Stderr, "--history must be between 0 and 10000000")
		os.Exit(2)
	}
	historyBytes, err := bytesize.Parse(*historyMemory)
	if err != nil || historyBytes < 1<<20 {
		fmt.Fprintln(os.Stderr, "--history-memory must be a size of at least 1MiB, such as 512MiB or 2GiB")
		os.Exit(2)
	}
	retainAge, err := retention.ParseAge(*historyRetain)
	if err != nil {
		fmt.Fprintln(os.Stderr, "--history-retain:", err)
		os.Exit(2)
	}
	retainSize, err := retention.ParseSize(*historyRetainSize)
	if err != nil {
		fmt.Fprintln(os.Stderr, "--history-retain-size:", err)
		os.Exit(2)
	}
	pasteBytes, err := bytesize.Parse(*pasteMax)
	if err != nil || pasteBytes < 1<<10 || pasteBytes > 8<<20 {
		fmt.Fprintln(os.Stderr, "--paste-max-size must be a size between 1KiB and 8MiB")
		os.Exit(2)
	}
	pasteTotalBytes, err := bytesize.Parse(*pasteTotal)
	if err != nil || pasteTotalBytes < pasteBytes {
		fmt.Fprintln(os.Stderr, "--paste-max-total must be a size of at least --paste-max-size")
		os.Exit(2)
	}
	pasteAge, err := retention.ParseAge(*pasteRetain)
	if err != nil {
		fmt.Fprintln(os.Stderr, "--paste-retain:", err)
		os.Exit(2)
	}
	transport, accessToken, err := serverTransport(*tlsCert, *tlsKey, *accessFile, *unixPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	paths, notes, err := resolveState(*stateDir, *historyFile, *adminTokenFile, *wakeAllow != "")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	var wakePrefixes []string
	if *wakeAllow != "" {
		if paths.accounts == "" {
			fmt.Fprintln(os.Stderr, "--wake-allow needs an accounts file: wake notices are for registered accounts")
			os.Exit(2)
		}
		wakePrefixes = strings.Split(*wakeAllow, ",")
	}
	if paths.history != "" && *history == 0 {
		*history = 10_000_000 // the window is bounded by --history-memory
	}
	logBackup := ""
	if *logFile != "" {
		logBackup = *logFile + ".1"
	}
	if err := pathcheck.Distinct(append(paths.list(), *adminTokenFile, *accessFile, *gatewayFile, *tlsKey, *tlsCert, *logFile, logBackup)...); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	logOutput := io.Writer(os.Stderr)
	if *logFile != "" {
		writer, err := openLog(*logFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		defer writer.Close()
		logOutput = writer
	}
	var handler slog.Handler
	if *logFormat == "json" {
		handler = slog.NewJSONHandler(logOutput, nil)
	} else {
		handler = slog.NewTextHandler(logOutput, nil)
	}
	logger := slog.New(handler)
	for _, note := range notes {
		if note.event == "state_file_shadowed" {
			logger.Warn(note.event, "path", note.path, "canonical", note.canonical)
		} else {
			logger.Info(note.event, "path", note.path, "canonical", note.canonical)
		}
	}
	srv := server.New(server.Config{TLSConfig: transport, HistoryLimit: *history, HistoryBytes: historyBytes, HistoryRetain: retainAge, HistoryRetainBytes: retainSize, MaxConnections: *maxConnections, MaxMessageSize: *maxMessage, OutboundBytes: *outboundBytes, Logger: logger, Sync: durability, Carriers: carriers, PasteBytes: int(pasteBytes), PasteTotalBytes: pasteTotalBytes, PasteRetain: pasteAge})
	if accessToken != "" {
		if err := srv.EnableAccess(accessToken); err != nil {
			logger.Error("access_config_failed", "error", err)
			os.Exit(1)
		}
	}
	if *gatewayFile != "" {
		token, err := admin.ReadToken(*gatewayFile)
		if err == nil {
			err = srv.EnableGateway(token)
		}
		if err != nil {
			logger.Error("gateway_config_failed", "error", err.Error())
			os.Exit(1)
		}
	}
	if paths.accounts != "" {
		if err := srv.RestoreAccounts(paths.accounts); err != nil {
			logger.Error("accounts_restore_failed", "error", err.Error())
			os.Exit(1)
		}
	}
	if paths.chat != "" {
		if err := srv.RestoreChat(paths.chat); err != nil {
			logger.Error("chat_restore_failed", "error", err.Error())
			os.Exit(1)
		}
	}
	if paths.handoffs != "" {
		if err := srv.RestoreHandoffs(paths.handoffs); err != nil {
			logger.Error("handoff_restore_failed", "error", err.Error())
			os.Exit(1)
		}
	}
	if paths.decisions != "" {
		if err := srv.RestoreDecisions(paths.decisions); err != nil {
			logger.Error("decision_restore_failed", "error", err.Error())
			os.Exit(1)
		}
	}
	if wakePrefixes != nil {
		if err := srv.EnableWake(wakePrefixes, paths.wakes, nil); err != nil {
			logger.Error("wake_config_failed", "error", err.Error())
			os.Exit(1)
		}
	}
	if err := configureAdmin(srv, *adminTokenFile, paths.moderation); err != nil {
		logger.Error("admin_config_failed", "error", err.Error())
		os.Exit(1)
	}
	if paths.history != "" {
		if err := srv.RestoreHistory(paths.history); err != nil {
			logger.Error("history_restore_failed", "error", err.Error())
			os.Exit(1)
		}
	}
	if paths.pastes != "" {
		// After history, so the sweep knows which sharing messages are retained.
		if err := srv.RestorePastes(paths.pastes); err != nil {
			logger.Error("paste_restore_failed", "error", err.Error())
			os.Exit(1)
		}
	}
	if paths.topics != "" {
		if err := srv.RestoreTopics(paths.topics); err != nil {
			logger.Error("topics_restore_failed", "error", err.Error())
			os.Exit(1)
		}
	}
	if paths.profiles != "" {
		if err := srv.RestoreProfiles(paths.profiles); err != nil {
			logger.Error("profiles_restore_failed", "error", err.Error())
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
