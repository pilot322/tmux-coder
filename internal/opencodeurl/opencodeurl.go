package opencodeurl

import (
	"encoding/base64"
	"net/url"
	"strings"
)

func Session(origin, sessionID string) string {
	origin = strings.TrimRight(origin, "/")
	serverID := base64.RawURLEncoding.EncodeToString([]byte(origin))
	return origin + "/server/" + serverID + "/session/" + url.PathEscape(sessionID)
}
