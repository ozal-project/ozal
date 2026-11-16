package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/ozal-project/ozal/internal/adminid"
)

// pausingRevocationStore keeps the real SQLite operation in flight while a
// second request tries to issue a device link from the session being revoked.
type pausingRevocationStore struct {
	identities
	entered, release chan struct{}
}

func (s *pausingRevocationStore) CompleteEnrolment(ctx context.Context, hash []byte, account adminid.Account, credential webauthn.Credential, label string) (adminid.Account, error) {
	close(s.entered)
	<-s.release
	return s.identities.CompleteEnrolment(ctx, hash, account, credential, label)
}

func (s *pausingRevocationStore) SetDisabled(ctx context.Context, id string, disabled bool) (adminid.Account, error) {
	close(s.entered)
	<-s.release
	return s.identities.SetDisabled(ctx, id, disabled)
}

func TestDeviceLinkIssuanceWaitsForAccountRevocation(t *testing.T) {
	for _, action := range []string{"recover", "disable"} {
		t.Run(action, func(t *testing.T) {
			h, _ := identityHandler(t)
			owner := ownerSession(t, h)
			invitation, invited := invite(t, h, owner, "ana@example.com", "people")
			if invited.Code != http.StatusCreated {
				t.Fatalf("invitation = %d", invited.Code)
			}
			joined := enrol(t, h, newAuthenticator(t), invitation, "Ana", "")
			if joined.Code != http.StatusOK {
				t.Fatalf("enrolment = %d", joined.Code)
			}
			session := decode(t, joined)["token"].(string)
			grant, ok := h.operators.authenticate(session)
			if !ok {
				t.Fatal("session not recognized")
			}
			account := h.operators.account(grant.ID)
			issued := api(h, http.MethodPost, "/_/api/admin/devices", session, nil)
			if issued.Code != http.StatusCreated {
				t.Fatalf("link before revocation = %d", issued.Code)
			}
			oldLink := decode(t, issued)["token"].(string)

			revoke := func() *httptest.ResponseRecorder {
				return api(h, http.MethodPost, "/_/api/admins/"+account+"/disable", owner, nil)
			}
			if action == "recover" {
				link, status := hostEnrolment(t, h, "recover", "ana@example.com")
				if status != http.StatusCreated {
					t.Fatalf("recovery link = %d", status)
				}
				begin := browser(h, "ozal.localhost:9091", "/_/api/admin/enrol/begin", "", map[string]string{"token": link})
				if begin.Code != http.StatusOK {
					t.Fatalf("recovery begin = %d", begin.Code)
				}
				started := decode(t, begin)
				credential := newAuthenticator(t).create(t, started["options"].(map[string]any), consoleOrigin)
				revoke = func() *httptest.ResponseRecorder {
					return browser(h, "ozal.localhost:9091", "/_/api/admin/enrol/finish", started["ceremony"].(string), credential)
				}
			}

			store := &pausingRevocationStore{identities: h.identities, entered: make(chan struct{}), release: make(chan struct{})}
			h.identities = store
			var release sync.Once
			resume := func() { release.Do(func() { close(store.release) }) }
			revoked := make(chan *httptest.ResponseRecorder, 1)
			revocationDone := make(chan struct{})
			go func() {
				defer close(revocationDone)
				revoked <- revoke()
			}()
			defer func() {
				resume()
				select {
				case <-revocationDone:
				case <-time.After(5 * time.Second):
					t.Error("revocation did not finish")
				}
			}()
			select {
			case <-store.entered:
			case <-revocationDone:
				t.Fatalf("revocation ended before storage: %d", (<-revoked).Code)
			case <-time.After(5 * time.Second):
				t.Fatal("revocation did not reach storage")
			}

			// Authentication still succeeds while the account's write is paused.
			// The clock callback signals that the competing HTTP request arrived;
			// it does not change the proof age or the session's validity.
			authenticated := make(chan struct{})
			var arrival sync.Once
			h.operators.now = func() time.Time {
				arrival.Do(func() { close(authenticated) })
				return time.Now()
			}
			linkResult := make(chan *httptest.ResponseRecorder, 1)
			linkDone := make(chan struct{})
			go func() {
				defer close(linkDone)
				linkResult <- api(h, http.MethodPost, "/_/api/admin/devices", session, nil)
			}()
			defer func() {
				resume()
				select {
				case <-linkDone:
				case <-time.After(5 * time.Second):
					t.Error("device-link request did not finish")
				}
			}()
			select {
			case <-authenticated:
			case <-time.After(5 * time.Second):
				t.Fatal("device-link request did not authenticate")
			}
			select {
			case result := <-linkResult:
				t.Fatalf("device-link request completed during revocation: %d", result.Code)
			case <-time.After(100 * time.Millisecond):
				// The request must wait until the in-flight revocation completes.
			}
			resume()
			<-revocationDone
			if result := <-revoked; result.Code != http.StatusOK {
				t.Fatalf("revocation = %d", result.Code)
			}
			<-linkDone
			if result := <-linkResult; result.Code != http.StatusForbidden {
				t.Fatalf("device link after session revocation = %d; want 403", result.Code)
			}
			if info := browser(h, "ozal.localhost:9091", "/_/api/admin/enrol/info", "", map[string]string{"token": oldLink}); info.Code != http.StatusUnauthorized {
				t.Fatalf("device link issued before revocation remains usable: %d", info.Code)
			}
			if result := api(h, http.MethodPost, "/_/api/admin/devices", session, nil); result.Code != http.StatusUnauthorized {
				t.Fatalf("revoked session can request another device link: %d", result.Code)
			}
		})
	}
}
