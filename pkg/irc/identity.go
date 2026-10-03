package irc

import "github.com/Someblueman/airc/internal/tokenfmt"

func validIdentityToken(token string) bool { return tokenfmt.Valid(token) }
