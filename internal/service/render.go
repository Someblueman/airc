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

func render(c Config, platform string) ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	argv := append([]string{c.Binary}, c.Args...)
	var b strings.Builder
	switch platform {
	case "darwin":
		b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
`)
		fmt.Fprintf(&b, "<key>Label</key><string>%s</string>\n<key>ProgramArguments</key><array>\n", xmlText(c.Label()))
		for _, arg := range argv {
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
`, xmlText(c.StateDir), xmlText(c.StateDir+"/startup.log"))
	case "linux":
		b.WriteString("[Unit]\nDescription=AIRC agent chat service\nAfter=network.target\n\n[Service]\nType=exec\nExecStart=")
		for i, arg := range argv {
			if i > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(unitArg(arg))
		}
		// WorkingDirectory performs specifier expansion, but not environment expansion.
		fmt.Fprintf(&b, "\nWorkingDirectory=%s\nRestart=on-failure\nRestartSec=10\nTimeoutStopSec=10\nUMask=0077\n\n[Install]\nWantedBy=default.target\n", strings.ReplaceAll(unitArg(c.StateDir), "$$", "$"))
		return []byte(b.String()), nil
	default:
		return nil, errors.New("unsupported service platform")
	}
	return []byte(b.String()), nil
}
