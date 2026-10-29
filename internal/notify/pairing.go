package notify

import (
	"crypto/rand"
	"crypto/subtle"
	"strings"
)

// pairingAlphabet has exactly 32 symbols — no I, O, 0 or 1, which are the
// ones people mistype — so 256 % 32 == 0 and the random bytes below map to
// it without bias.
const (
	pairingAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	pairingCodeLen  = 8
)

// NewPairingCode returns a one-time code for linking a chat. Eight symbols
// of a 32-symbol alphabet are 40 bits: unguessable within the short life of
// a pairing command, and short enough to type on a phone.
func NewPairingCode() (string, error) {
	raw := make([]byte, pairingCodeLen)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	code := make([]byte, pairingCodeLen)
	for i, b := range raw {
		code[i] = pairingAlphabet[int(b)%len(pairingAlphabet)]
	}
	return string(code), nil
}

// PairCommand extracts the code from a pairing message such as "/pair
// ABC234" or "/pair@ozal_bot ABC234" (Telegram appends the bot name
// in groups). The code is upper-cased; anything else is not a pairing
// attempt.
func PairCommand(text string) (string, bool) {
	fields := strings.Fields(text)
	if len(fields) != 2 || !strings.HasPrefix(fields[0], "/") {
		return "", false
	}
	command := strings.TrimPrefix(fields[0], "/")
	if at := strings.IndexByte(command, '@'); at >= 0 {
		command = command[:at]
	}
	if command != "pair" {
		return "", false
	}
	code := strings.ToUpper(fields[1])
	if !validPairingCode(code) {
		return "", false
	}
	return code, true
}

func validPairingCode(code string) bool {
	if len(code) != pairingCodeLen {
		return false
	}
	for _, symbol := range code {
		if !strings.ContainsRune(pairingAlphabet, symbol) {
			return false
		}
	}
	return true
}

// CodeEqual compares a supplied code with the expected one in constant time,
// so a wrong guess reveals nothing about how wrong it was.
func CodeEqual(expected, provided string) bool {
	return subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) == 1
}
