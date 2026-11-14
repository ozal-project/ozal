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
	if got := accepted("example.test", []byte("handle"), [][]byte{a, b}, a); got == nil || len(got.IDs) != 2 || got.UserID != "aGFuZGxl" {
		t.Fatalf("whole list = %+v", got)
	}
	for name, got := range map[string]*acceptedCredentials{
		"empty":               accepted("example.test", []byte("handle"), nil, nil),
		"without the sign-in": accepted("example.test", []byte("handle"), [][]byte{b}, a),
		"no relying party":    accepted("", []byte("handle"), [][]byte{a}, nil),
		"no user handle":      accepted("example.test", nil, [][]byte{a}, nil),
		"an empty credential": accepted("example.test", []byte("handle"), [][]byte{a, {}}, nil),
	} {
		if got != nil {
			t.Errorf("%s: sent %+v", name, got)
		}
	}
}

// Only a definite "revoked here" names a passkey as gone.
func TestRetiredOnlyForARevokedPasskey(t *testing.T) {
	id := []byte("credential-a-0000")
	if got := retired("example.test", id, true, nil); got == nil || got.CredentialID != "Y3JlZGVudGlhbC1hLTAwMDA" {
		t.Fatalf("revoked = %+v", got)
	}
	for name, got := range map[string]*unknownCredential{
		"active or never held": retired("example.test", id, false, nil),
		"failed check":         retired("example.test", id, true, errors.New("down")),
		"no credential":        retired("example.test", nil, true, nil),
		"no relying party":     retired("", id, true, nil),
	} {
		if got != nil {
			t.Errorf("%s: named %+v", name, got)
		}
	}
}
