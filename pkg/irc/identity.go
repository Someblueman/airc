package irc

import "github.com/Someblueman/airc/internal/admin"

func validIdentityToken(token string) bool { return admin.ValidToken(token) }
