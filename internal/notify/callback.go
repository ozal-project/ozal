package notify

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Telegram caps an inline button's callback_data at 64 bytes. The signature
// therefore covers a compact payload — action, nonce, expiry — and the
// context of the decision (who, what, why) stays in the pending record the
// nonce addresses, never in the button. The tag is the first 16 bytes of
// HMAC-SHA256: 128 bits is the standard short-tag choice and it is what
// fits.
const (
	callbackMACBytes = 16
	// CallbackMaxLen is Telegram's limit; SignCallback never exceeds it.
	CallbackMaxLen = 64
)

var (
	// ErrCallbackInvalid means the data is malformed or not authentic.
	ErrCallbackInvalid = errors.New("notify: callback data is not authentic")
	// ErrCallbackExpired means the data was authentic but too old.
	ErrCallbackExpired = errors.New("notify: callback data has expired")
)

// Callback is the compact data an inline button carries. Nonce identifies
// the pending decision server-side, which is also what makes a single-use
// check possible: consuming a nonce exactly once belongs to the approver
// that resolves the pending record, together with the expiry checked here.
type Callback struct {
	Action    string
	Nonce     string
	ExpiresAt int64
}

// CallbackKey derives the signing key from the bot token with a domain
// separator, so the token is never used directly as a second protocol's key
// and rotating the bot token invalidates every outstanding button.
func CallbackKey(token string) []byte {
	sum := sha256.Sum256([]byte("switchboard/notify-callback/v1\x00" + token))
	return sum[:]
}

// SignCallback returns "action:nonce:expiry.mac" with a URL-safe tag. It
// refuses a payload that could not be verified again (empty or malformed
// fields, or one that would exceed the Telegram limit).
func SignCallback(key []byte, cb Callback) (string, error) {
	if !validAction(cb.Action) {
		return "", fmt.Errorf("%w: action %q", ErrCallbackInvalid, cb.Action)
	}
	if cb.Nonce == "" || strings.ContainsRune(cb.Nonce, ':') {
		return "", fmt.Errorf("%w: nonce", ErrCallbackInvalid)
	}
	if cb.ExpiresAt <= 0 {
		return "", fmt.Errorf("%w: expiry", ErrCallbackInvalid)
	}
	payload := cb.Action + ":" + cb.Nonce + ":" + strconv.FormatInt(cb.ExpiresAt, 10)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	value := payload + "." + base64URL(mac.Sum(nil)[:callbackMACBytes])
	if len(value) > CallbackMaxLen {
		return "", fmt.Errorf("%w: signed data is %d bytes, Telegram allows %d", ErrCallbackInvalid, len(value), CallbackMaxLen)
	}
	return value, nil
}

// VerifyCallback authenticates callback data and checks its expiry. Any
// failure other than expiry is the same error: a caller must not tell an
// attacker which check failed.
func VerifyCallback(key []byte, value string, now time.Time) (Callback, error) {
	payload, tag, ok := strings.Cut(value, ".")
	if !ok {
		return Callback{}, ErrCallbackInvalid
	}
	provided, err := base64URLDecode(tag)
	if err != nil {
		return Callback{}, ErrCallbackInvalid
	}
	if len(provided) != callbackMACBytes {
		return Callback{}, ErrCallbackInvalid
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	if !hmac.Equal(mac.Sum(nil)[:callbackMACBytes], provided) {
		return Callback{}, ErrCallbackInvalid
	}
	action, rest, ok := strings.Cut(payload, ":")
	if !ok {
		return Callback{}, ErrCallbackInvalid
	}
	nonce, expiry, ok := strings.Cut(rest, ":")
	if !ok || !validAction(action) || nonce == "" {
		return Callback{}, ErrCallbackInvalid
	}
	expiresAt, err := strconv.ParseInt(expiry, 10, 64)
	if err != nil || expiresAt <= 0 {
		return Callback{}, ErrCallbackInvalid
	}
	if now.Unix() > expiresAt {
		return Callback{}, ErrCallbackExpired
	}
	return Callback{Action: action, Nonce: nonce, ExpiresAt: expiresAt}, nil
}

// validAction keeps the delimiter out of the action and bounds it to a
// readable identifier.
func validAction(action string) bool {
	if action == "" || len(action) > 16 {
		return false
	}
	for _, r := range action {
		if (r < 'a' || r > 'z') && r != '_' && r != '-' {
			return false
		}
	}
	return true
}
