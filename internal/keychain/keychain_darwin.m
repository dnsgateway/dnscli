//go:build darwin && cgo

#import <Foundation/Foundation.h>
#import <LocalAuthentication/LocalAuthentication.h>
#import <Security/Security.h>

#include <stdlib.h>
#include <string.h>

#include "keychain_darwin.h"

static NSString *const kDnscliService = @"dnscli";
static NSString *const kDnscliLabel = @"dnscli Unseal key";

// errSecMissingEntitlement: the binary may not use the data protection keychain.
static const OSStatus kMissingEntitlement = -34018;

static char *copyString(NSString *s) {
	if (s == nil) {
		return strdup("unknown error");
	}

	return strdup(s.UTF8String);
}

static char *statusMessage(OSStatus status) {
	CFStringRef msg = SecCopyErrorMessageString(status, NULL);
	if (msg == NULL) {
		return copyString([NSString stringWithFormat:@"OSStatus %d", (int)status]);
	}

	char *out = copyString((__bridge NSString *)msg);
	CFRelease(msg);

	return out;
}

// itemQuery matches dnscli's item for account in the data protection
// keychain, the only keychain where an access control is enforced, or in
// the login keychain for an item a devkeychain build stored.
static NSMutableDictionary *itemQuery(const char *account, int login) {
	NSMutableDictionary *q = [NSMutableDictionary dictionary];
	q[(__bridge id)kSecClass] = (__bridge id)kSecClassGenericPassword;
	q[(__bridge id)kSecAttrService] = kDnscliService;
	q[(__bridge id)kSecAttrAccount] = [NSString stringWithUTF8String:account];
	if (!login) {
		q[(__bridge id)kSecUseDataProtectionKeychain] = @YES;
	}

	return q;
}

int dnscli_keychain_store(const char *account, const void *secret, int secret_len, int allow_login, int *in_login, char **err) {
	@autoreleasepool {
		// An item that requires Touch ID is useless, and cannot be made,
		// where Touch ID cannot be evaluated, such as on a Mac without it.
		LAContext *context = [[LAContext alloc] init];
		NSError *laErr = nil;
		if (![context canEvaluatePolicy:LAPolicyDeviceOwnerAuthenticationWithBiometrics error:&laErr]) {
			*err = copyString(laErr.localizedDescription);

			return DNSCLI_KEYCHAIN_NO_BIOMETRY;
		}

		// Only Touch ID with the currently enrolled fingers releases the
		// item, enforced by the Secure Enclave.
		CFErrorRef acErr = NULL;
		SecAccessControlRef access = SecAccessControlCreateWithFlags(kCFAllocatorDefault,
			kSecAttrAccessibleWhenUnlockedThisDeviceOnly, kSecAccessControlBiometryCurrentSet, &acErr);
		if (access == NULL) {
			NSError *e = (__bridge_transfer NSError *)acErr;
			*err = copyString(e.localizedDescription);

			return DNSCLI_KEYCHAIN_NO_PROTECTION;
		}

		NSData *data = [NSData dataWithBytes:secret length:(NSUInteger)secret_len];

		NSMutableDictionary *attrs = itemQuery(account, 0);
		attrs[(__bridge id)kSecValueData] = data;
		attrs[(__bridge id)kSecAttrLabel] = kDnscliLabel;
		attrs[(__bridge id)kSecAttrAccessControl] = (__bridge_transfer id)access;

		OSStatus status = SecItemAdd((__bridge CFDictionaryRef)attrs, NULL);
		if (status == errSecSuccess) {
			*in_login = 0;

			return 0;
		}

		// A binary without the keychain-access-groups entitlement, such as
		// one built with go build, cannot use the data protection keychain.
		// Only a devkeychain build falls back to the login keychain, where
		// the item is guarded by dnscli's own Touch ID check, not by the OS.
		if (status == kMissingEntitlement && allow_login) {
			NSMutableDictionary *login = itemQuery(account, 1);
			login[(__bridge id)kSecValueData] = data;
			login[(__bridge id)kSecAttrLabel] = kDnscliLabel;

			status = SecItemAdd((__bridge CFDictionaryRef)login, NULL);
			if (status == errSecSuccess) {
				*in_login = 1;

				return 0;
			}
		}

		*err = statusMessage(status);

		if (status == kMissingEntitlement) {
			return DNSCLI_KEYCHAIN_NOT_ENTITLED;
		}

		return (int)status;
	}
}

int dnscli_keychain_load(const char *account, int login, const char *reason, void **secret, int *secret_len, char **err) {
	@autoreleasepool {
		LAContext *context = [[LAContext alloc] init];
		// The fallback button returns to dnscli, which asks for the passphrase.
		context.localizedFallbackTitle = @"Use Passphrase";

		NSError *laErr = nil;
		if (![context canEvaluatePolicy:LAPolicyDeviceOwnerAuthenticationWithBiometrics error:&laErr]) {
			*err = copyString(laErr.localizedDescription);

			return DNSCLI_KEYCHAIN_NO_BIOMETRY;
		}

		__block BOOL ok = NO;
		__block NSError *evalErr = nil;
		dispatch_semaphore_t done = dispatch_semaphore_create(0);

		[context evaluatePolicy:LAPolicyDeviceOwnerAuthenticationWithBiometrics
			localizedReason:[NSString stringWithUTF8String:reason]
			reply:^(BOOL success, NSError *error) {
				ok = success;
				evalErr = error;
				dispatch_semaphore_signal(done);
			}];

		dispatch_semaphore_wait(done, DISPATCH_TIME_FOREVER);

		if (!ok) {
			*err = copyString(evalErr.localizedDescription);

			switch (evalErr.code) {
			case LAErrorUserCancel:
			case LAErrorSystemCancel:
			case LAErrorAppCancel:
			case LAErrorUserFallback:
				return DNSCLI_KEYCHAIN_CANCELED;
			default:
				return DNSCLI_KEYCHAIN_AUTH_FAILED;
			}
		}

		NSMutableDictionary *query = itemQuery(account, login);
		query[(__bridge id)kSecReturnData] = @YES;
		query[(__bridge id)kSecMatchLimit] = (__bridge id)kSecMatchLimitOne;
		if (!login) {
			// The evaluated context satisfies the item's Touch ID access
			// control, so the user is not asked twice.
			query[(__bridge id)kSecUseAuthenticationContext] = context;
		}

		CFTypeRef result = NULL;
		OSStatus status = SecItemCopyMatching((__bridge CFDictionaryRef)query, &result);
		if (status != errSecSuccess) {
			*err = statusMessage(status);

			return (int)status;
		}

		NSData *data = (__bridge_transfer NSData *)result;
		*secret_len = (int)data.length;
		*secret = malloc(data.length > 0 ? data.length : 1);
		memcpy(*secret, data.bytes, data.length);

		return 0;
	}
}

int dnscli_keychain_delete(const char *account, int login, char **err) {
	@autoreleasepool {
		OSStatus status = SecItemDelete((__bridge CFDictionaryRef)itemQuery(account, login));
		if (status == errSecSuccess || status == errSecItemNotFound) {
			return 0;
		}

		*err = statusMessage(status);

		return (int)status;
	}
}
