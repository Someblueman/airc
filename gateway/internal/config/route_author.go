package config

// BoundAuthor is the AIRC author a mailbox listener posts as when a request
// names none, for one-shot producers whose files would otherwise keep tokens
// at rest. With an account token file the author is that registered account
// (the gateway holds its token, as a client would); without one it is a
// guest under that nickname. The gateway is not an identity authority: AIRC
// still decides whether the author may post.
type BoundAuthor struct {
	Nick             string `json:"nick"`
	AccountTokenFile string `json:"account_token_file,omitempty"`
}

// boundAuthorFiles lists every configured bound author token file.
func (c Config) boundAuthorFiles() []string {
	var paths []string
	for _, l := range c.Listeners {
		if b := l.BoundAuthor; b != nil && b.AccountTokenFile != "" {
			paths = append(paths, b.AccountTokenFile)
		}
	}
	return paths
}
