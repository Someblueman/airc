package gateway_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/backend"
)

func TestHTTPDirectoryNicknameNativeQueryAndUnknownCard(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(fmt.Sprint("http2=", h2), func(t *testing.T) {
			f := gatewayHTTPFixture(t, h2)
			for _, nick := range []string{"FiXtUrE-AuThOr", "fixture-observer", "unpublished-nick"} {
				status, _, data := httpCall(t, f, http.MethodGet, "/v1/directory?limit=1&nick="+url.QueryEscape(nick), f.token, nil)
				var snapshot backend.DirectorySnapshot
				if status != 200 || json.Unmarshal(data, &snapshot) != nil || len(snapshot.Agents) != 1 {
					t.Fatal("native nickname query failed", nick, status, string(data))
				}
				card := snapshot.Agents[0]
				switch nick {
				case "FiXtUrE-AuThOr":
					if card.Nick != "fixture-author" || card.AccountID == "" || card.Connected {
						t.Fatal("author query changed native card", card)
					}
				case "fixture-observer":
					if card.Nick != nick || !card.Connected {
						t.Fatal("observer query changed connection fact", card)
					}
				default:
					if card.Nick != nick || card.State != "unknown" || card.Connected || card.AccountID != "" || !card.LastSeen.IsZero() || card.About != "" {
						t.Fatal("unknown nickname invented public facts", card)
					}
				}
			}
			for _, query := range []string{"nick=", "nick=agent&nick=other", "nick=%23room", "nick=agent,other", "nick=agent%0D%0AOPER", "nick=%2A", "nick=1agent", "nick=agent&query=other"} {
				status, _, data := httpCall(t, f, http.MethodGet, "/v1/directory?"+query, f.token, nil)
				if status != 400 {
					t.Fatal("invalid nickname query admitted", query, status, string(data))
				}
			}
			status, _, _ := httpCall(t, f, http.MethodGet, "/v1/directory?nick=fixture-author", wrongAuthorSpec, nil)
			if status != 401 {
				t.Fatal("nickname lookup with a wrong token", status)
			}
		})
	}
}

func TestDirectoryNicknameRejectsWrongOrMissingNativeCard(t *testing.T) {
	for _, mode := range []string{"directory-wrong-nick", "directory-empty"} {
		t.Run(mode, func(t *testing.T) {
			f := gatewayHTTPFixture(t, true, mode)
			status, _, data := httpCall(t, f, http.MethodGet, "/v1/directory?nick=fixture-author", f.token, nil)
			var result map[string]json.RawMessage
			_ = json.Unmarshal(data, &result)
			if status != 503 || result["agents"] != nil {
				t.Fatal("uncorrelated nickname snapshot exposed", mode, status, string(data))
			}
		})
	}
}
