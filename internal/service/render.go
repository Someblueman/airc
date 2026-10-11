package service

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"strings"
)

func xmlText(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
func unitArg(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, `%`, `%%`)
	s = strings.ReplaceAll(s, `$`, `$$`)
	return `"` + s + `"`
}

// job is one process-manager job: aircd, or the bundled gateway.
type job struct {
	label, description string
	argv               []string
	errorLog           string
	// after is the systemd unit this job wants and starts after.
	after       string
	stopTimeout int
}

func (c Config) daemonJob() job {
	return job{label: c.Label(), description: "AIRC agent chat service", argv: append([]string{c.Binary}, c.Args...), errorLog: c.StateDir + "/startup.log", stopTimeout: 10}
}

// gatewayJob runs the gateway as its own process, so a gateway fault never
// takes aircd down. Its HTTP shutdown and delivery workers each get five
// seconds, hence the longer stop timeout.
func (c Config) gatewayJob() job {
	return job{label: c.GatewayLabel(), description: "AIRC gateway carriers", argv: []string{c.Gateway.Binary, "serve", "--file", c.GatewayConfigPath()}, errorLog: c.GatewayLogPath(), after: c.Label() + ".service", stopTimeout: 20}
}

// jobs are in start order: aircd first, then the gateway.
func (c Config) jobs() []job {
	if c.Gateway == nil {
		return []job{c.daemonJob()}
	}
	return []job{c.daemonJob(), c.gatewayJob()}
}

func render(c Config, platform string) ([]byte, error) { return renderJob(c, c.daemonJob(), platform) }

func renderJob(c Config, j job, platform string) ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	var b strings.Builder
	switch platform {
	case "darwin":
		b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
`)
		fmt.Fprintf(&b, "<key>Label</key><string>%s</string>\n<key>ProgramArguments</key><array>\n", xmlText(j.label))
		for _, arg := range j.argv {
			fmt.Fprintf(&b, "<string>%s</string>\n", xmlText(arg))
		}
		fmt.Fprintf(&b, `</array>
<key>WorkingDirectory</key><string>%s</string>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
<key>ThrottleInterval</key><integer>10</integer>
<key>ProcessType</key><string>Background</string>
<key>Umask</key><integer>63</integer>
<key>StandardOutPath</key><string>/dev/null</string>
<key>StandardErrorPath</key><string>%s</string>
</dict></plist>
`, xmlText(c.StateDir), xmlText(j.errorLog))
	case "linux":
		fmt.Fprintf(&b, "[Unit]\nDescription=%s\n", j.description)
		if j.after != "" {
			fmt.Fprintf(&b, "Wants=%s\nAfter=network.target %s\n", j.after, j.after)
		} else {
			b.WriteString("After=network.target\n")
		}
		b.WriteString("\n[Service]\nType=exec\nExecStart=")
		for i, arg := range j.argv {
			if i > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(unitArg(arg))
		}
		// WorkingDirectory performs specifier expansion, but not environment expansion.
		fmt.Fprintf(&b, "\nWorkingDirectory=%s\nRestart=on-failure\nRestartSec=10\nTimeoutStopSec=%d\nUMask=0077\n\n[Install]\nWantedBy=default.target\n", strings.ReplaceAll(unitArg(c.StateDir), "$$", "$"), j.stopTimeout)
	default:
		return nil, errors.New("unsupported service platform")
	}
	return []byte(b.String()), nil
}
