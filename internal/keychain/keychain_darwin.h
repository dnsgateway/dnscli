// The C bridge from Go to the macOS Keychain and LocalAuthentication.
// Plain C types only, so the Go side needs no Apple headers.

#ifndef DNSCLI_KEYCHAIN_DARWIN_H
#define DNSCLI_KEYCHAIN_DARWIN_H

// Results besides 0 (success) and a negative OSStatus from Security.framework.
#define DNSCLI_KEYCHAIN_CANCELED 1      // the user canceled Touch ID
#define DNSCLI_KEYCHAIN_NO_BIOMETRY 2   // Touch ID is not available or not set up
#define DNSCLI_KEYCHAIN_AUTH_FAILED 3   // Touch ID did not recognise the user
#define DNSCLI_KEYCHAIN_NOT_ENTITLED 4  // the binary may not use the data protection keychain
#define DNSCLI_KEYCHAIN_NO_PROTECTION 5 // the Touch ID access control could not be made

// dnscli_keychain_store adds a generic password item holding secret to the
// data protection keychain, with an access control that only Touch ID with
// the currently enrolled fingers satisfies. It stores nothing when Touch ID
// cannot be evaluated. When the binary lacks the keychain entitlement it
// stores nothing either, unless allow_login is set (devkeychain builds only):
// then the item goes into the login keychain, where only dnscli's own Touch
// ID check guards it, and *in_login is set to 1. On failure it returns
// non-zero and sets *err to a malloc'd message.
int dnscli_keychain_store(const char *account, const void *secret, int secret_len, int allow_login, int *in_login, char **err);

// dnscli_keychain_load asks for Touch ID, showing reason, and then reads the
// item. On success it sets *secret to malloc'd bytes and *secret_len; on
// failure it returns non-zero and sets *err to a malloc'd message.
// login selects the login keychain, for an item a devkeychain build stored.
int dnscli_keychain_load(const char *account, int login, const char *reason, void **secret, int *secret_len, char **err);

// dnscli_keychain_delete deletes the item without asking for Touch ID. An
// item that does not exist is not an error. On failure it returns non-zero
// and sets *err to a malloc'd message.
int dnscli_keychain_delete(const char *account, int login, char **err);

#endif
