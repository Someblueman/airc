package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

const uiUsage = "usage: airc ui [--nick NAME] [--channel #room[,#room...]] [--backlog 100]"

// runUI is a full-screen client in the style of the classic IRC clients: channels
// on the left, the conversation in the middle with the channel header above it,
// who is around on the right, and an input line below.
func runUI(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("airc ui", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	channels := fs.String("channel", os.Getenv("AIRC_CHANNEL"), "channels to open first (default: every channel on the server)")
	backlog := fs.Int("backlog", 100, "messages to load per channel (0-1000)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *backlog < 0 || *backlog > 1000 {
		return errors.New(uiUsage)
	}
	if opt.nick == "" {
		opt.nick = os.Getenv("AIRC_NICK")
	}
	if opt.nick == "" {
		opt.nick = os.Getenv("USER")
	}
	if opt.nick == "" {
		opt.nick = "viewer"
	}
	out, ok := stdout.(*os.File)
	if !ok || !isTerminal(out) || !isTerminal(os.Stdin) {
		return errors.New("airc ui needs an interactive terminal; use `airc watch` to stream to a pipe")
	}

	var initial []string
	for _, name := range strings.Split(*channels, ",") {
		if name = channelName(name); isChannel(name) {
			initial = append(initial, name)
		}
	}
	model := newUIModel(opt.nick, initial, time.Now)
	msgs := make(chan any, 512)
	cmds := make(chan uiCmd, 32)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	restore, err := enterRawMode(os.Stdin)
	if err != nil {
		return err
	}
	defer restore()
	fmt.Fprint(out, enterScreen)
	defer fmt.Fprint(out, leaveScreen)

	cols, rows := terminalSize(out)
	model.update(resizeIn{max(cols, 30), max(rows, 6)})
	backend := &uiBackend{opt: *opt, nick: opt.nick, initial: initial, backlog: *backlog, out: msgs, cmds: cmds}
	go backend.run(ctx)
	go readKeys(ctx, msgs)
	go watchResize(ctx, out, msgs)
	go tick(ctx, msgs)

	draw := func(clear bool) {
		frame := strings.Builder{}
		frame.WriteString(hideCursor)
		if clear {
			frame.WriteString("\x1b[2J")
		}
		for i, row := range model.view() {
			fmt.Fprintf(&frame, "\x1b[%d;1H%s", i+1, row)
		}
		row, col := model.cursorPosition()
		fmt.Fprintf(&frame, "\x1b[%d;%dH%s", row, col, showCursor)
		_, _ = io.WriteString(out, frame.String())
	}
	draw(true)
	for {
		var msg any
		select {
		case msg = <-msgs:
		case <-ctx.Done():
			return nil
		}
		clear := false
		// Handle everything that is already waiting, then draw once.
		for more := true; more; {
			if fatal, isFatal := msg.(fatalIn); isFatal {
				return fatal.err
			}
			if _, resized := msg.(resizeIn); resized {
				clear = true
			}
			wanted, quit := model.update(msg)
			if quit {
				return nil
			}
			for _, cmd := range wanted {
				select {
				case cmds <- cmd:
				default:
					model.setStatus("busy; try again", true)
				}
			}
			select {
			case msg = <-msgs:
			default:
				more = false
			}
		}
		draw(clear)
	}
}

func readKeys(ctx context.Context, msgs chan<- any) {
	buf := make([]byte, 256)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			return
		}
		for _, k := range parseKeys(buf[:n]) {
			select {
			case msgs <- keyIn(k):
			case <-ctx.Done():
				return
			}
		}
	}
}

func watchResize(ctx context.Context, out *os.File, msgs chan<- any) {
	changed := make(chan os.Signal, 1)
	signal.Notify(changed, syscall.SIGWINCH)
	defer signal.Stop(changed)
	for {
		select {
		case <-changed:
			cols, rows := terminalSize(out)
			select {
			case msgs <- resizeIn{max(cols, 30), max(rows, 6)}:
			case <-ctx.Done():
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func tick(ctx context.Context, msgs chan<- any) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			select {
			case msgs <- tickIn{}:
			default: // the UI is busy; the next tick will do
			}
		case <-ctx.Done():
			return
		}
	}
}
