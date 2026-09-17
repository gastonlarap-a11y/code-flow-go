//go:build darwin

package security

import (
	"errors"

	"github.com/keybase/go-keychain"
)

// The macOS backend, built on the SecItem API.
//
// The item shape is the contract, and it is narrow on purpose: class `kSecClassGenericPassword`,
// a service, an account, and nothing else. No label, no access group, no
// `kSecUseDataProtectionKeychain` — every one of those becomes part of what the query has to match,
// and 2.x set none of them. An item written with an extra attribute is invisible to a query
// without it, which would look exactly like "the user never saved a token".
//
// Chosen over zalando/go-keyring, which shells out to `/usr/bin/security add-generic-password`.
// That works, but the item's ACL owner is then the `security` binary rather than CodeFlow, so
// every read would prompt regardless of what the user allowed.
func osBackend() backend { return keychainBackend{} }

type keychainBackend struct{}

func (keychainBackend) get(service, key string) (string, error) {
	query := keychain.NewItem()
	query.SetSecClass(keychain.SecClassGenericPassword)
	query.SetService(service)
	query.SetAccount(key)
	query.SetMatchLimit(keychain.MatchLimitOne)
	query.SetReturnData(true)

	results, err := keychain.QueryItem(query)
	if err != nil {
		return "", mapKeychainError(err)
	}
	if len(results) == 0 {
		// A missing account returns an empty result rather than an error, so this is the "none"
		// path — not a failure.
		return "", ErrNoEntry
	}
	return string(results[0].Data), nil
}

func (k keychainBackend) set(service, key, secret string) error {
	item := keychain.NewItem()
	item.SetSecClass(keychain.SecClassGenericPassword)
	item.SetService(service)
	item.SetAccount(key)
	item.SetData([]byte(secret))

	query := keychain.NewItem()
	query.SetSecClass(keychain.SecClassGenericPassword)
	query.SetService(service)
	query.SetAccount(key)

	// Update first, add on miss. The other order would need a delete between them, and a crash in
	// that window loses the credential the user had.
	err := keychain.UpdateItem(query, item)
	if err == nil {
		return nil
	}
	if errors.Is(err, keychain.ErrorItemNotFound) {
		if err := keychain.AddItem(item); err != nil {
			return mapKeychainError(err)
		}
		return nil
	}
	return mapKeychainError(err)
}

func (keychainBackend) delete(service, key string) error {
	item := keychain.NewItem()
	item.SetSecClass(keychain.SecClassGenericPassword)
	item.SetService(service)
	item.SetAccount(key)

	if err := keychain.DeleteItem(item); err != nil {
		return mapKeychainError(err)
	}
	return nil
}

// mapKeychainError sorts an OSStatus into the three outcomes the store distinguishes.
//
// The refusal group matters most. After an update the app's ad-hoc code signature changes, so the
// keychain partition no longer matches and macOS asks for the login password before handing over
// a secret it previously allowed. Whatever the user does with that dialog arrives here:
// ErrorAuthFailed, ErrorInteractionNotAllowed or ErrorUserCanceled. All three mean "ask again
// later", which is what CREDENTIAL_REFUSED tells the renderer to offer — and none of them means
// the credential is gone.
func mapKeychainError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, keychain.ErrorItemNotFound):
		return ErrNoEntry
	case errors.Is(err, keychain.ErrorAuthFailed),
		errors.Is(err, keychain.ErrorInteractionNotAllowed),
		errors.Is(err, keychain.ErrorUserCanceled):
		return refused(err.Error())
	default:
		return err
	}
}
