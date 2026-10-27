package admin

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
)

// virtualAuthenticator plays the device side of WebAuthn the way a real
// passkey does: an ES256 key per credential, authenticator data with the
// RP ID hash and flags, and a signature over authenticatorData ||
// SHA-256(clientDataJSON). Tests change its fields to act like an attacker
// (another origin, a counter going backwards, no user verification).
type virtualAuthenticator struct {
	key          *ecdsa.PrivateKey
	credentialID []byte
	userHandle   []byte
	counter      uint32
	// Flags reported in every response.
	userVerified   bool
	backupEligible bool
}

func newAuthenticator(t *testing.T) *virtualAuthenticator {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 32)
	if _, err := rand.Read(id); err != nil {
		t.Fatal(err)
	}
	return &virtualAuthenticator{key: key, credentialID: id, userVerified: true}
}

const (
	flagUP = 0x01
	flagUV = 0x04
	flagBE = 0x08
	flagAT = 0x40
)

func (a *virtualAuthenticator) authData(rpID string, attested bool) []byte {
	rpHash := sha256.Sum256([]byte(rpID))
	flags := byte(flagUP)
	if a.userVerified {
		flags |= flagUV
	}
	if a.backupEligible {
		flags |= flagBE
	}
	if attested {
		flags |= flagAT
	}
	data := append([]byte{}, rpHash[:]...)
	data = append(data, flags)
	data = binary.BigEndian.AppendUint32(data, a.counter)
	if attested {
		data = append(data, make([]byte, 16)...) // AAGUID: none
		data = binary.BigEndian.AppendUint16(data, uint16(len(a.credentialID)))
		data = append(data, a.credentialID...)
		point, err := a.key.PublicKey.Bytes() // 0x04 || X || Y
		if err != nil {
			panic(err)
		}
		x, y := point[1:33], point[33:]
		// COSE_Key: kty EC2 (2), alg ES256 (-7), crv P-256 (1), x, y.
		cose, err := webauthncbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: x, -3: y})
		if err != nil {
			panic(err)
		}
		data = append(data, cose...)
	}
	return data
}

func clientData(kind, challenge, origin string) []byte {
	body, _ := json.Marshal(map[string]any{"type": kind, "challenge": challenge, "origin": origin, "crossOrigin": false})
	return body
}

var b64 = base64.RawURLEncoding

// create answers navigator.credentials.create() with "none" attestation.
func (a *virtualAuthenticator) create(t *testing.T, options map[string]any, origin string) map[string]any {
	t.Helper()
	pk := options["publicKey"].(map[string]any)
	rpID := pk["rp"].(map[string]any)["id"].(string)
	user := pk["user"].(map[string]any)
	handle, err := b64.DecodeString(user["id"].(string))
	if err != nil {
		t.Fatalf("user.id: %v", err)
	}
	a.userHandle = handle
	attestation, err := webauthncbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": a.authData(rpID, true)})
	if err != nil {
		t.Fatal(err)
	}
	id := b64.EncodeToString(a.credentialID)
	return map[string]any{
		"id": id, "rawId": id, "type": "public-key",
		"clientExtensionResults": map[string]any{},
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(clientData("webauthn.create", pk["challenge"].(string), origin)),
			"attestationObject": b64.EncodeToString(attestation),
			"transports":        []string{"internal"},
		},
	}
}

// get answers navigator.credentials.get() for a discoverable login.
func (a *virtualAuthenticator) get(t *testing.T, options map[string]any, origin string) map[string]any {
	t.Helper()
	pk := options["publicKey"].(map[string]any)
	rpID := pk["rpId"].(string)
	a.counter++
	data := a.authData(rpID, false)
	client := clientData("webauthn.get", pk["challenge"].(string), origin)
	clientHash := sha256.Sum256(client)
	digest := sha256.Sum256(append(append([]byte{}, data...), clientHash[:]...))
	signature, err := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	id := b64.EncodeToString(a.credentialID)
	return map[string]any{
		"id": id, "rawId": id, "type": "public-key",
		"clientExtensionResults": map[string]any{},
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(client),
			"authenticatorData": b64.EncodeToString(data),
			"signature":         b64.EncodeToString(signature),
			"userHandle":        b64.EncodeToString(a.userHandle),
		},
	}
}
