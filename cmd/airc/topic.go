package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

const topicUsage = "usage: airc topic #channel [--set TEXT | --clear] [--json]"

// fetchTopic reads a channel's header. other, if set, sees every unrelated event
// so a caller that is also observing does not lose live notifications.
func fetchTopic(client *irc.Client, channel string, other func(irc.Event)) (string, error) {
	if err := client.Topic(channel); err != nil {
		return "", err
	}
	return awaitTopic(client, channel, other)
}

func awaitTopic(client *irc.Client, channel string, other func(irc.Event)) (string, error) {
	timer := time.NewTimer(requestTimeout)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				return "", errors.New("server disconnected while reading the topic")
			}
			if topic, isTopic := event.(*irc.TopicEvent); isTopic && topic.Channel == channel {
				return topic.Topic, nil
			}
			if err := serverError(event); err != nil {
				return "", err
			}
			if other != nil {
				other(event)
			}
		case <-timer.C:
			return "", errors.New("timed out waiting for the topic")
		case <-client.Done():
			return "", errors.New("connection closed while reading the topic")
		}
	}
}

// runTopic reads, sets or clears a channel's header, the line shown at the top
// of the channel and to agents the first time they check it.
func runTopic(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return errors.New(topicUsage)
	}
	channel := channelName(args[0])
	fs := flag.NewFlagSet("airc topic", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	set := fs.String("set", "", "set the header to this text (up to 400 bytes)")
	clear := fs.Bool("clear", false, "remove the header")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 || (*set != "" && *clear) || !isChannel(channel) {
		return errors.New(topicUsage)
	}
	changing := *set != "" || *clear
	if changing {
		if err := identity(opt); err != nil {
			return err
		}
	} else if opt.nick == "" {
		opt.nick = defaultQueryNick()
	}
	client, err := dialOneShot(*opt)
	if err != nil {
		return err
	}
	defer client.Close()
	if !client.Supports("TOPIC") {
		return errors.New("this aircd predates channel topics; restart it from a current build")
	}
	var topic string
	if changing {
		if err := client.SetTopic(channel, strings.TrimSpace(*set)); err != nil {
			return err
		}
		if topic, err = awaitTopic(client, channel, nil); err != nil {
			return fmt.Errorf("set topic of %s: %w", channel, err)
		}
	} else if topic, err = fetchTopic(client, channel, nil); err != nil {
		return fmt.Errorf("read topic of %s: %w", channel, err)
	}
	if opt.json {
		return json.NewEncoder(stdout).Encode(irc.TopicEvent{Type: "topic", Channel: channel, Topic: topic})
	}
	switch {
	case changing && topic == "":
		_, err = fmt.Fprintf(stdout, "cleared the topic of %s\n", channel)
	case changing:
		_, err = fmt.Fprintf(stdout, "set the topic of %s: %s\n", channel, topic)
	case topic == "":
		_, err = fmt.Fprintf(stdout, "%s has no topic\n", channel)
	default:
		_, err = fmt.Fprintln(stdout, topic)
	}
	return err
}
