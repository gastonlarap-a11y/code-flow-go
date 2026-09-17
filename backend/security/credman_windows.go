//go:build windows

package security

import (
	"errors"

	"github.com/danieljoos/wincred"
)

// The Windows backend, on Credential Manager.
//
// The target name is `{service}.{key}` — "com.codeflow.app.github-token:github.com" — which is
// what 2.x's WindowsCredentialManager.cs wrote. That dot is the whole compatibility story:
// zalando/go-keyring builds its target as `service + ":" + username` instead, so adopting it
// would make every credential an existing user saved unreadable, silently, while the app reported
// "not configured".
func osBackend() backend { return credmanBackend{} }

type credmanBackend struct{}

func target(service, key string) string { return service + "." + key }

func (credmanBackend) get(service, key string) (string, error) {
	credential, err := wincred.GetGenericCredential(target(service, key))
	if err != nil {
		if errors.Is(err, wincred.ErrElementNotFound) {
			return "", ErrNoEntry
		}
		return "", refused(err.Error())
	}
	return string(credential.CredentialBlob), nil
}

func (credmanBackend) set(service, key, secret string) error {
	credential := wincred.NewGenericCredential(target(service, key))

	// UserName is set to the key rather than left empty because that is what 2.x stored, and it is
	// what shows in the Credential Manager UI — an entry with a blank user name is one the user
	// cannot identify when they go looking.
	credential.UserName = key
	credential.CredentialBlob = []byte(secret)

	// Persist defaults to LocalMachine in wincred, which is what NewGenericCredential sets and
	// what a real 2.7.1 entry carries (Persist=2). Left alone deliberately.
	if err := credential.Write(); err != nil {
		return refused(err.Error())
	}
	return nil
}

func (credmanBackend) delete(service, key string) error {
	credential, err := wincred.GetGenericCredential(target(service, key))
	if err != nil {
		if errors.Is(err, wincred.ErrElementNotFound) {
			return ErrNoEntry
		}
		return refused(err.Error())
	}
	if err := credential.Delete(); err != nil {
		return refused(err.Error())
	}
	return nil
}
