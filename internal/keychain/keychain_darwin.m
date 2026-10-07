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

static NSMutableDictionary *itemQuery(const char *account, BOOL dataProtection) {
	NSMutableDictionary *q = [NSMutableDictionary dictionary];
	q[(__bridge id)kSecClass] = (__bridge id)kSecClassGenericPassword;
	q[(__bridge id)kSecAttrService] = kDnscliService;
	q[(__bridge id)kSecAttrAccount] = [NSString stringWithUTF8String:account];

	if (dataProtection) {
		q[(__bridge id)kSecUseDataProtectionKeychain] = @YES;
	}

	return q;
}

int dnscli_keychain_store(const char *account, const void *secret, int secret_len, int *data_protection, char **err) {
	@autoreleasepool {
		NSData *data = [NSData dataWithBytes:secret length:(NSUInteger)secret_len];
		*data_protection = 0;

		// Preferred: only Touch ID with the currently enrolled fingers
		// releases the item, enforced by the Secure Enclave.
		CFErrorRef acErr = NULL;
		SecAccessControlRef access = SecAccessControlCreateWithFlags(kCFAllocatorDefault,
			kSecAttrAccessibleWhenUnlockedThisDeviceOnly, kSecAccessControlBiometryCurrentSet, &acErr);

		if (access != NULL) {
			NSMutableDictionary *attrs = itemQuery(account, YES);
			attrs[(__bridge id)kSecValueData] = data;
			attrs[(__bridge id)kSecAttrLabel] = kDnscliLabel;
			attrs[(__bridge id)kSecAttrAccessControl] = (__bridge_transfer id)access;

			OSStatus status = SecItemAdd((__bridge CFDictionaryRef)attrs, NULL);
			if (status == errSecSuccess) {
				*data_protection = 1;

				return 0;
			}

			if (status != kMissingEntitlement) {
				*err = statusMessage(status);

				return (int)status;
			}
		} else if (acErr != NULL) {
			CFRelease(acErr);
		}

		// A binary without the keychain-access-groups entitlement, such as
		// one built with go build, cannot use the data protection keychain.
		// The item goes into the login keychain instead, and
		// dnscli_keychain_load asks for Touch ID before reading it.
		NSMutableDictionary *attrs = itemQuery(account, NO);
		attrs[(__bridge id)kSecValueData] = data;
		attrs[(__bridge id)kSecAttrLabel] = kDnscliLabel;

		OSStatus status = SecItemAdd((__bridge CFDictionaryRef)attrs, NULL);
		if (status != errSecSuccess) {
			*err = statusMessage(status);

			return (int)status;
		}

		return 0;
	}
}

int dnscli_keychain_load(const char *account, int data_protection, const char *reason, void **secret, int *secret_len, char **err) {
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

		NSMutableDictionary *query = itemQuery(account, data_protection != 0);
		query[(__bridge id)kSecReturnData] = @YES;
		query[(__bridge id)kSecMatchLimit] = (__bridge id)kSecMatchLimitOne;

		if (data_protection) {
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
