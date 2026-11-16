package passkey

import "encoding/base64"

// The WebAuthn Signal API lets a page tell the browser or password manager
// which of this site's passkeys are still good, so a revoked one stops being
// offered. A wrong signal can hide a passkey that still works, so the server
// only ever sends what it read for certain, and nothing when it is unsure.
// The page sends it without waiting: a browser without the API, or one that
// ignores it, changes nothing, and a resolved promise does not prove the
// manager acted.

// AcceptedCredentials is the complete list of one account's active passkeys
// on this relying party, for signalAllAcceptedCredentials.
type AcceptedCredentials struct {
	RPID   string   `json:"rp_id"`
	UserID string   `json:"user_id"`
	IDs    []string `json:"ids"`
}

// AcceptedPasskeys builds that list, or nil when it would not be safe to send: no
// relying party, no user handle, or no passkeys (an empty list tells a
// manager to hide every passkey of the account). must, when set, is a
// passkey that has to be in the list, such as the one that just signed in;
// a list without it was read at the wrong moment and is not sent.
func AcceptedPasskeys(rpID string, handle []byte, ids [][]byte, must []byte) *AcceptedCredentials {
	if rpID == "" || len(handle) == 0 || len(ids) == 0 {
		return nil
	}
	out := &AcceptedCredentials{RPID: rpID, UserID: base64.RawURLEncoding.EncodeToString(handle)}
	found := must == nil
	for _, id := range ids {
		if len(id) == 0 {
			return nil
		}
		if string(id) == string(must) {
			found = true
		}
		out.IDs = append(out.IDs, base64.RawURLEncoding.EncodeToString(id))
	}
	if !found {
		return nil
	}
	return out
}

// UnknownCredential names one passkey this site has retired, for
// signalUnknownCredential.
type UnknownCredential struct {
	RPID         string `json:"rp_id"`
	CredentialID string `json:"credential_id"`
}

// RetiredPasskey decides whether a refused sign-in may tell the browser that its
// passkey is gone. Only a definite answer counts: the directory holds this
// passkey and revoked it. A passkey it never held (an administrator's passkey
// used here, or another directory's), one still active (a disabled account, a
// bad signature) or a failed check is ambiguous, and nothing is sent.
func RetiredPasskey(rpID string, credentialID []byte, revoked bool, err error) *UnknownCredential {
	if rpID == "" || len(credentialID) == 0 || err != nil || !revoked {
		return nil
	}
	return &UnknownCredential{RPID: rpID, CredentialID: base64.RawURLEncoding.EncodeToString(credentialID)}
}
