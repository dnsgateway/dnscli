// The C bridge from Go to the macOS Keychain and LocalAuthentication.
// Plain C types only, so the Go side needs no Apple headers.

#ifndef DNSCLI_KEYCHAIN_DARWIN_H
#define DNSCLI_KEYCHAIN_DARWIN_H

// Results besides 0 (success) and a negative OSStatus from Security.framework.
#define DNSCLI_KEYCHAIN_CANCELED 1     // the user canceled Touch ID
#define DNSCLI_KEYCHAIN_NO_BIOMETRY 2  // Touch ID is not available or not set up
#define DNSCLI_KEYCHAIN_AUTH_FAILED 3  // Touch ID did not recognise the user

// dnscli_keychain_store adds a generic password item holding secret. It
// tries the data protection keychain with a Touch ID access control first,
// and sets *data_protection to 1 when that worked; a binary without the
// keychain entitlement uses the login keychain instead. On failure it
// returns non-zero and sets *err to a malloc'd message.
int dnscli_keychain_store(const char *account, const void *secret, int secret_len, int *data_protection, char **err);

// dnscli_keychain_load asks for Touch ID, showing reason, and then reads the
// item. On success it sets *secret to malloc'd bytes and *secret_len; on
// failure it returns non-zero and sets *err to a malloc'd message.
int dnscli_keychain_load(const char *account, int data_protection, const char *reason, void **secret, int *secret_len, char **err);

#endif
