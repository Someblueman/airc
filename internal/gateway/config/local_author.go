package config

import (
	"errors"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// localAuthorCarrier lists the local and offline carriers that take plain
// author requests. Their access gate is the kernel or owner-only boundary
// they already had (socket and file permissions, peer credentials, the
// parent process, the local operator who runs an import); every request
// names its author, and AIRC's per-source limits see listener:<id>.
func localAuthorCarrier(protocol string) bool {
	switch protocol {
	case "unixgram", "unixpacket", "stdio", "socketpair", "inherited", "fifo", "uart",
		"file_mailbox", "directory_mailbox", "append_log",
		"posix_mqueue", "system_v_queue", "shared_memory", "mapped_file",
		"mach_ipc", "offline_bundle", "bpv7":
		return true
	}
	return false
}

// boundAuthorCarrier lists carriers that may configure a bound author:
// one-shot carriers whose producers would otherwise keep tokens at rest.
func boundAuthorCarrier(protocol string) bool {
	switch protocol {
	case "file_mailbox", "directory_mailbox", "append_log", "posix_mqueue", "system_v_queue", "shared_memory", "mapped_file", "offline_bundle", "bpv7":
		return true
	}
	return false
}

// replyCarrier lists packet carriers whose plain replies go to one
// owner-provisioned reply destination (reply_to) instead of the source:
// their producers have no address of their own.
func replyCarrier(protocol string) bool {
	switch protocol {
	case "posix_mqueue", "system_v_queue", "shared_memory", "mapped_file":
		return true
	}
	return false
}

func (c Config) validateLocalAuthor(l Listener) error {
	if l.BoundAuthor != nil {
		if !boundAuthorCarrier(l.Protocol) {
			return errors.New("bound_author requires a mailbox, queue, shared memory, mapped file or offline listener")
		}
		if !(core.Author{Nick: l.BoundAuthor.Nick}).Valid() {
			return errors.New("bound_author requires a valid nickname")
		}
		if l.BoundAuthor.AccountTokenFile != "" && !absolute(l.BoundAuthor.AccountTokenFile) {
			return errors.New("bound_author: invalid account token file path")
		}
	}
	if l.ReplyTo == "" {
		if l.Enabled && replyCarrier(l.Protocol) {
			return errors.New("a queue, shared memory or mapped file listener requires reply_to")
		}
		return nil
	}
	if !replyCarrier(l.Protocol) {
		return errors.New("reply_to requires a POSIX queue, System V queue, shared memory or mapped file listener")
	}
	ok := false
	switch l.Protocol {
	case "posix_mqueue":
		ok = posixQueueName(l.ReplyTo) && l.ReplyTo != l.Address
	case "system_v_queue":
		reply, err := SystemVQueueKey(l.ReplyTo)
		ingress, ingressErr := SystemVQueueKey(l.Address)
		ok = err == nil && ingressErr == nil && reply != ingress
	case "shared_memory":
		ok = sharedMemoryName(l.ReplyTo) && l.ReplyTo != l.Address
	case "mapped_file":
		ok = mappedDirectory(l.ReplyTo) && !sharedRootsOverlap(l.ReplyTo, l.Address) && !sharedRootsOverlap(l.ReplyTo, c.StateDir)
	}
	if !ok {
		return errors.New("reply_to must name a distinct destination of the listener's own kind")
	}
	return nil
}
