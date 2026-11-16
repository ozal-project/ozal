package passkey

import (
	"errors"
	"testing"
)

// A list is sent only whole: never empty (that hides every passkey of the
// account), never without the passkey that just signed in, never without a
// relying party or a user handle.
func TestAcceptedListIsCompleteOrAbsent(t *testing.T) {
	a, b := []byte("credential-a-0000"), []byte("credential-b-0000")
	if got := AcceptedPasskeys("example.test", []byte("handle"), [][]byte{a, b}, a); got == nil || len(got.IDs) != 2 || got.UserID != "aGFuZGxl" {
		t.Fatalf("whole list = %+v", got)
	}
	for name, got := range map[string]*AcceptedCredentials{
		"empty":               AcceptedPasskeys("example.test", []byte("handle"), nil, nil),
		"without the sign-in": AcceptedPasskeys("example.test", []byte("handle"), [][]byte{b}, a),
		"no relying party":    AcceptedPasskeys("", []byte("handle"), [][]byte{a}, nil),
		"no user handle":      AcceptedPasskeys("example.test", nil, [][]byte{a}, nil),
		"an empty credential": AcceptedPasskeys("example.test", []byte("handle"), [][]byte{a, {}}, nil),
	} {
		if got != nil {
			t.Errorf("%s: sent %+v", name, got)
		}
	}
}

// Only a definite "revoked here" names a passkey as gone.
func TestRetiredOnlyForARevokedPasskey(t *testing.T) {
	id := []byte("credential-a-0000")
	if got := RetiredPasskey("example.test", id, true, nil); got == nil || got.CredentialID != "Y3JlZGVudGlhbC1hLTAwMDA" {
		t.Fatalf("revoked = %+v", got)
	}
	for name, got := range map[string]*UnknownCredential{
		"active or never held": RetiredPasskey("example.test", id, false, nil),
		"failed check":         RetiredPasskey("example.test", id, true, errors.New("down")),
		"no credential":        RetiredPasskey("example.test", nil, true, nil),
		"no relying party":     RetiredPasskey("", id, true, nil),
	} {
		if got != nil {
			t.Errorf("%s: named %+v", name, got)
		}
	}
}
